package metrics

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/scheduler"
	"github.com/prometheus/prometheus/prompb"
	"go.uber.org/zap"
)

// Pusher handles pushing metrics to Prometheus remote_write endpoint
type Pusher struct {
	url          string
	username     string
	password     string
	client       *http.Client
	logger       *zap.Logger
	lastPush     time.Time
	buffer       *buffer.RingBuffer
	pushInterval time.Duration
	batchSize    int
}

// New creates a new Prometheus pusher
func New(url, username, password string, buf *buffer.RingBuffer, pushIntervalSeconds, batchSize int, logger *zap.Logger) *Pusher {
	return &Pusher{
		url:      url,
		username: username,
		password: password,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:       logger,
		lastPush:     time.Now(),
		buffer:       buf,
		pushInterval: time.Duration(pushIntervalSeconds) * time.Second,
		batchSize:    batchSize,
	}
}

// Start begins the periodic metrics pushing in a goroutine
func (p *Pusher) Start(ctx context.Context) {
	p.logger.Info("prometheus pusher started",
		zap.Duration("push_interval", p.pushInterval),
		zap.Int("batch_size", p.batchSize),
	)

	// Wait for first aligned interval
	initialWait := scheduler.WaitForAlignedInterval(p.pushInterval, p.logger)
	select {
	case <-ctx.Done():
		p.logger.Info("prometheus pusher stopped before first push")
		return
	case <-time.After(initialWait):
		// Push at aligned time
		p.pushMetrics()
	}

	// Now use ticker for subsequent runs
	ticker := time.NewTicker(p.pushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("prometheus pusher stopping")
			return
		case <-ticker.C:
			p.pushMetrics()
		}
	}
}

// pushMetrics gets all readings from buffer and pushes them in batches
func (p *Pusher) pushMetrics() {
	// Get all readings and clear buffer atomically
	readings := p.buffer.GetAllAndClear()
	if len(readings) == 0 {
		p.logger.Debug("no readings to push")
		return
	}

	p.logger.Debug("pushing metrics to prometheus",
		zap.Int("total_readings", len(readings)),
		zap.Int("batch_size", p.batchSize),
	)

	// Process readings in batches
	totalBatches := (len(readings) + p.batchSize - 1) / p.batchSize
	ctx := context.Background() // Use background context for push operation

	for batchNum := 0; batchNum < totalBatches; batchNum++ {
		start := batchNum * p.batchSize
		end := start + p.batchSize
		if end > len(readings) {
			end = len(readings)
		}
		batch := readings[start:end]

		p.logger.Debug("pushing batch",
			zap.Int("batch_number", batchNum+1),
			zap.Int("total_batches", totalBatches),
			zap.Int("batch_readings", len(batch)),
		)

		err := p.Push(ctx, batch)
		if err != nil {
			p.logger.Error("failed to push batch, re-adding remaining readings to buffer",
				zap.Error(err),
				zap.Int("batch_number", batchNum+1),
				zap.Int("failed_readings", len(readings)-start),
			)
			// Re-add the failed batch and all remaining batches
			p.buffer.AddMultiple(readings[start:])
			break
		}

		p.logger.Debug("successfully pushed batch",
			zap.Int("batch_number", batchNum+1),
			zap.Int("batch_readings", len(batch)),
		)
	}
}

// Push pushes sensor readings to Prometheus
func (p *Pusher) Push(ctx context.Context, readings []*buffer.Reading) error {
	if len(readings) == 0 {
		p.logger.Debug("no readings to push")
		return nil
	}

	// Build write request
	writeReq, err := p.buildWriteRequest(readings)
	if err != nil {
		return fmt.Errorf("failed to build write request: %w", err)
	}

	// Try to push with retries
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		err := p.pushOnce(ctx, writeReq)
		if err == nil {
			p.lastPush = time.Now()

			bleCount := 0
			netatmoCount := 0
			powerCount := 0
			controlCount := 0
			weightedAvgCount := 0
			for _, r := range readings {
				switch r.Type {
				case buffer.ReadingTypeBLE:
					bleCount++
				case buffer.ReadingTypeNetatmo:
					netatmoCount++
				case buffer.ReadingTypePower:
					powerCount++
				case buffer.ReadingTypeControl:
					controlCount++
				case buffer.ReadingTypeBLEWeightedAvg:
					weightedAvgCount++
				}
			}

			p.logger.Info("successfully pushed metrics",
				zap.Int("ble_data_points", bleCount),
				zap.Int("netatmo_data_points", netatmoCount),
				zap.Int("power_data_points", powerCount),
				zap.Int("control_data_points", controlCount),
				zap.Int("weighted_avg_data_points", weightedAvgCount),
				zap.Int("total_data_points", len(readings)),
				zap.Int("attempt", attempt),
			)
			return nil
		}

		lastErr = err
		p.logger.Warn("failed to push metrics, will retry",
			zap.Int("attempt", attempt),
			zap.Error(err),
		)

		// Exponential backoff: 1s, 2s, 4s
		if attempt < 3 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	return fmt.Errorf("failed to push metrics after 3 attempts: %w", lastErr)
}

// buildWriteRequest converts sensor readings to Prometheus WriteRequest
func (p *Pusher) buildWriteRequest(readings []*buffer.Reading) (*prompb.WriteRequest, error) {
	var timeSeries []prompb.TimeSeries

	// Separate BLE, Netatmo, Power, Control, and Weighted Average readings
	var bleReadings []*buffer.SensorReading
	var netatmoReadings []*buffer.ThermostatReading
	var powerReadings []*buffer.PowerReading
	var controlReadings []*buffer.ControlReading
	var weightedAvgReadings []*buffer.WeightedAvgReading

	for _, reading := range readings {
		switch reading.Type {
		case buffer.ReadingTypeBLE:
			if reading.BLE != nil {
				bleReadings = append(bleReadings, reading.BLE)
			}
		case buffer.ReadingTypeNetatmo:
			if reading.Thermostat != nil {
				netatmoReadings = append(netatmoReadings, reading.Thermostat)
			}
		case buffer.ReadingTypePower:
			if reading.Power != nil {
				powerReadings = append(powerReadings, reading.Power)
			}
		case buffer.ReadingTypeControl:
			if reading.Control != nil {
				controlReadings = append(controlReadings, reading.Control)
			}
		case buffer.ReadingTypeBLEWeightedAvg:
			if reading.WeightedAvg != nil {
				weightedAvgReadings = append(weightedAvgReadings, reading.WeightedAvg)
			}
		}
	}

	// Process BLE readings
	bleSeries, err := p.buildBLETimeSeries(bleReadings)
	if err != nil {
		return nil, fmt.Errorf("failed to build BLE time series: %w", err)
	}
	timeSeries = append(timeSeries, bleSeries...)

	// Process Netatmo readings
	netatmoSeries, err := p.buildNetatmoTimeSeries(netatmoReadings)
	if err != nil {
		return nil, fmt.Errorf("failed to build Netatmo time series: %w", err)
	}
	timeSeries = append(timeSeries, netatmoSeries...)

	// Process Power readings
	powerSeries, err := p.buildPowerTimeSeries(powerReadings)
	if err != nil {
		return nil, fmt.Errorf("failed to build Power time series: %w", err)
	}
	timeSeries = append(timeSeries, powerSeries...)

	// Process Control readings
	controlSeries, err := p.buildControlTimeSeries(controlReadings)
	if err != nil {
		return nil, fmt.Errorf("failed to build Control time series: %w", err)
	}
	timeSeries = append(timeSeries, controlSeries...)

	// Process Weighted Average readings
	weightedAvgSeries, err := p.buildWeightedAvgTimeSeries(weightedAvgReadings)
	if err != nil {
		return nil, fmt.Errorf("failed to build Weighted Average time series: %w", err)
	}
	timeSeries = append(timeSeries, weightedAvgSeries...)

	return &prompb.WriteRequest{
		Timeseries: timeSeries,
	}, nil
}

// buildBLETimeSeries builds time series for BLE sensor readings
func (p *Pusher) buildBLETimeSeries(readings []*buffer.SensorReading) ([]prompb.TimeSeries, error) {
	// Group readings by sensor
	type sensorKey struct {
		name string
		id   int
	}
	sensorReadings := make(map[sensorKey][]*buffer.SensorReading)
	for _, reading := range readings {
		key := sensorKey{name: reading.SensorName, id: reading.SensorID}
		sensorReadings[key] = append(sensorReadings[key], reading)
	}

	// Build time series for each sensor and metric
	var timeSeries []prompb.TimeSeries
	for key, sensorData := range sensorReadings {
		// Create base labels for this sensor
		baseLabels := []prompb.Label{
			{
				Name:  "sensor_name",
				Value: key.name,
			},
			{
				Name:  "sensor_id",
				Value: fmt.Sprintf("%d", key.id),
			},
			{
				Name:  "mac",
				Value: sensorData[0].MAC, // All readings have same MAC
			},
		}

		// Temperature time series
		tempSamples := make([]prompb.Sample, 0, len(sensorData))
		humiditySamples := make([]prompb.Sample, 0, len(sensorData))
		batterySamples := make([]prompb.Sample, 0, len(sensorData))

		for _, reading := range sensorData {
			// Round timestamp to nearest 10 seconds, then convert to milliseconds
			ts, ok := reading.Timestamp.(time.Time)
			if !ok {
				p.logger.Warn("invalid timestamp type in reading",
					zap.String("sensor_name", key.name),
				)
				continue
			}
			roundedTime := roundToTenSeconds(ts)
			timestampMs := roundedTime.UnixMilli()

			// Add temperature sample
			tempSamples = append(tempSamples, prompb.Sample{
				Value:     reading.TemperatureCelsius,
				Timestamp: timestampMs,
			})

			// Add humidity sample
			humiditySamples = append(humiditySamples, prompb.Sample{
				Value:     float64(reading.HumidityPercent),
				Timestamp: timestampMs,
			})

			// Add battery sample
			batterySamples = append(batterySamples, prompb.Sample{
				Value:     float64(reading.BatteryPercent),
				Timestamp: timestampMs,
			})
		}

		// Add temperature time series
		tempLabels := append([]prompb.Label{
			{
				Name:  "__name__",
				Value: "ble_temperature_celsius",
			},
		}, baseLabels...)
		timeSeries = append(timeSeries, prompb.TimeSeries{
			Labels:  tempLabels,
			Samples: tempSamples,
		})

		// Add humidity time series
		humidityLabels := append([]prompb.Label{
			{
				Name:  "__name__",
				Value: "ble_humidity_percent",
			},
		}, baseLabels...)
		timeSeries = append(timeSeries, prompb.TimeSeries{
			Labels:  humidityLabels,
			Samples: humiditySamples,
		})

		// Add battery time series
		batteryLabels := append([]prompb.Label{
			{
				Name:  "__name__",
				Value: "ble_battery_percent",
			},
		}, baseLabels...)
		timeSeries = append(timeSeries, prompb.TimeSeries{
			Labels:  batteryLabels,
			Samples: batterySamples,
		})
	}

	return timeSeries, nil
}

// buildNetatmoTimeSeries builds time series for Netatmo thermostat readings
func (p *Pusher) buildNetatmoTimeSeries(readings []*buffer.ThermostatReading) ([]prompb.TimeSeries, error) {
	// Group readings by room
	type roomKey struct {
		homeID   string
		homeName string
		roomID   string
		roomName string
	}
	roomReadings := make(map[roomKey][]*buffer.ThermostatReading)
	for _, reading := range readings {
		key := roomKey{
			homeID:   reading.HomeID,
			homeName: reading.HomeName,
			roomID:   reading.RoomID,
			roomName: reading.RoomName,
		}
		roomReadings[key] = append(roomReadings[key], reading)
	}

	// Build time series for each room and metric
	var timeSeries []prompb.TimeSeries
	for key, roomData := range roomReadings {
		// Create base labels for this room
		baseLabels := []prompb.Label{
			{
				Name:  "home_id",
				Value: key.homeID,
			},
			{
				Name:  "room_id",
				Value: key.roomID,
			},
			{
				Name:  "room_name",
				Value: key.roomName,
			},
		}

		// Prepare samples
		measuredTempSamples := make([]prompb.Sample, 0, len(roomData))
		setpointTempSamples := make([]prompb.Sample, 0, len(roomData))
		heatingPowerSamples := make([]prompb.Sample, 0, len(roomData))

		for _, reading := range roomData {
			// Round timestamp to nearest 10 seconds, then convert to milliseconds
			ts, ok := reading.Timestamp.(time.Time)
			if !ok {
				p.logger.Warn("invalid timestamp type in netatmo reading",
					zap.String("room_name", key.roomName),
				)
				continue
			}
			roundedTime := roundToTenSeconds(ts)
			timestampMs := roundedTime.UnixMilli()

			// Add measured temperature sample
			measuredTempSamples = append(measuredTempSamples, prompb.Sample{
				Value:     reading.MeasuredTemperature,
				Timestamp: timestampMs,
			})

			// Add setpoint temperature sample
			setpointTempSamples = append(setpointTempSamples, prompb.Sample{
				Value:     reading.SetpointTemperature,
				Timestamp: timestampMs,
			})

			// Add heating power request sample
			heatingPowerSamples = append(heatingPowerSamples, prompb.Sample{
				Value:     float64(reading.HeatingPowerRequest),
				Timestamp: timestampMs,
			})
		}

		// Add measured temperature time series
		measuredTempLabels := append([]prompb.Label{
			{
				Name:  "__name__",
				Value: "netatmo_measured_temperature_celsius",
			},
		}, baseLabels...)
		timeSeries = append(timeSeries, prompb.TimeSeries{
			Labels:  measuredTempLabels,
			Samples: measuredTempSamples,
		})

		// Add setpoint temperature time series
		setpointTempLabels := append([]prompb.Label{
			{
				Name:  "__name__",
				Value: "netatmo_setpoint_temperature_celsius",
			},
		}, baseLabels...)
		timeSeries = append(timeSeries, prompb.TimeSeries{
			Labels:  setpointTempLabels,
			Samples: setpointTempSamples,
		})

		// Add heating power request time series
		heatingPowerLabels := append([]prompb.Label{
			{
				Name:  "__name__",
				Value: "netatmo_heating_power_request",
			},
		}, baseLabels...)
		timeSeries = append(timeSeries, prompb.TimeSeries{
			Labels:  heatingPowerLabels,
			Samples: heatingPowerSamples,
		})
	}

	return timeSeries, nil
}

// pushOnce attempts to push the write request once
func (p *Pusher) pushOnce(ctx context.Context, writeReq *prompb.WriteRequest) error {
	// Marshal to protobuf
	data, err := proto.Marshal(writeReq)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}

	// Compress with snappy
	compressed := snappy.Encode(nil, data)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	// Set basic auth
	if p.username != "" && p.password != "" {
		req.SetBasicAuth(p.username, p.password)
	}

	// Send request
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("received non-2xx status code: %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// LastPushTime returns the time of the last successful push
func (p *Pusher) LastPushTime() time.Time {
	return p.lastPush
}

// buildPowerTimeSeries builds time series for power meter readings
func (p *Pusher) buildPowerTimeSeries(readings []*buffer.PowerReading) ([]prompb.TimeSeries, error) {
	// Group readings by sensor ID and sensor type (e.g., sensor 0 with active_power, sensor 0 with voltage)
	type sensorKey struct {
		id         int
		sensorType string
	}
	sensorReadings := make(map[sensorKey][]*buffer.PowerReading)
	for _, reading := range readings {
		key := sensorKey{
			id:         reading.SensorID,
			sensorType: reading.SensorType,
		}
		sensorReadings[key] = append(sensorReadings[key], reading)
	}

	// Build time series for each sensor and type
	var timeSeries []prompb.TimeSeries
	for key, sensorData := range sensorReadings {
		// Create labels for this sensor and type
		labels := []prompb.Label{
			{
				Name:  "__name__",
				Value: key.sensorType,
			},
			{
				Name:  "sensor_id",
				Value: fmt.Sprintf("%d", key.id),
			},
		}

		// Add sensor name label if available
		if len(sensorData) > 0 && sensorData[0].SensorName != "" {
			labels = append(labels, prompb.Label{
				Name:  "sensor_name",
				Value: sensorData[0].SensorName,
			})
		}

		// Prepare samples
		samples := make([]prompb.Sample, 0, len(sensorData))

		for _, reading := range sensorData {
			ts, ok := reading.Timestamp.(time.Time)
			if !ok {
				p.logger.Warn("invalid timestamp type in power reading",
					zap.Int("sensor_id", key.id),
					zap.String("sensor_type", key.sensorType),
				)
				continue
			}
			timestampMs := ts.UnixMilli()

			// Add sample
			samples = append(samples, prompb.Sample{
				Value:     reading.Value,
				Timestamp: timestampMs,
			})
		}

		// Add time series
		timeSeries = append(timeSeries, prompb.TimeSeries{
			Labels:  labels,
			Samples: samples,
		})
	}

	return timeSeries, nil
}

// buildControlTimeSeries builds time series for control loop metrics
func (p *Pusher) buildControlTimeSeries(readings []*buffer.ControlReading) ([]prompb.TimeSeries, error) {
	// Group readings by room
	roomReadings := make(map[string][]*buffer.ControlReading)
	for _, reading := range readings {
		roomReadings[reading.RoomName] = append(roomReadings[reading.RoomName], reading)
	}

	var timeSeries []prompb.TimeSeries

	for roomName, roomData := range roomReadings {
		// Create base labels for this room
		baseLabels := []prompb.Label{
			{Name: "room_name", Value: roomName},
		}

		// Prepare samples for different metrics
		xiaomiTempSamples := make([]prompb.Sample, 0, len(roomData))
		scheduledTempSamples := make([]prompb.Sample, 0, len(roomData))
		thermostatMeasuredSamples := make([]prompb.Sample, 0, len(roomData))
		calculatedSetpointSamples := make([]prompb.Sample, 0, len(roomData))
		tempDiffSamples := make([]prompb.Sample, 0, len(roomData))
		setpointAdjSamples := make([]prompb.Sample, 0, len(roomData))
		actionSamples := make([]prompb.Sample, 0, len(roomData))

		for _, reading := range roomData {
			ts, ok := reading.Timestamp.(time.Time)
			if !ok {
				continue
			}
			roundedTime := roundToTenSeconds(ts)
			timestampMs := roundedTime.UnixMilli()

			xiaomiTempSamples = append(xiaomiTempSamples, prompb.Sample{
				Value:     reading.XiaomiTemperature,
				Timestamp: timestampMs,
			})

			scheduledTempSamples = append(scheduledTempSamples, prompb.Sample{
				Value:     reading.ScheduledTemperature,
				Timestamp: timestampMs,
			})

			thermostatMeasuredSamples = append(thermostatMeasuredSamples, prompb.Sample{
				Value:     reading.ThermostatMeasured,
				Timestamp: timestampMs,
			})

			calculatedSetpointSamples = append(calculatedSetpointSamples, prompb.Sample{
				Value:     reading.CalculatedSetpoint,
				Timestamp: timestampMs,
			})

			tempDiffSamples = append(tempDiffSamples, prompb.Sample{
				Value:     reading.TemperatureDifference,
				Timestamp: timestampMs,
			})

			setpointAdjSamples = append(setpointAdjSamples, prompb.Sample{
				Value:     reading.SetpointAdjustment,
				Timestamp: timestampMs,
			})

			// Convert action to numeric value
			actionValue := 0.0
			switch reading.Action {
			case "skip":
				actionValue = 0.0
			case "no_adjustment_needed":
				actionValue = 1.0
			case "set_manual_override":
				actionValue = 2.0
			}
			actionSamples = append(actionSamples, prompb.Sample{
				Value:     actionValue,
				Timestamp: timestampMs,
			})
		}

		// Build time series for each metric
		timeSeries = append(timeSeries,
			prompb.TimeSeries{
				Labels:  append(baseLabels, prompb.Label{Name: "__name__", Value: "thermostat_control_xiaomi_temperature_celsius"}),
				Samples: xiaomiTempSamples,
			},
			prompb.TimeSeries{
				Labels:  append(baseLabels, prompb.Label{Name: "__name__", Value: "thermostat_control_scheduled_temperature_celsius"}),
				Samples: scheduledTempSamples,
			},
			prompb.TimeSeries{
				Labels:  append(baseLabels, prompb.Label{Name: "__name__", Value: "thermostat_control_measured_temperature_celsius"}),
				Samples: thermostatMeasuredSamples,
			},
			prompb.TimeSeries{
				Labels:  append(baseLabels, prompb.Label{Name: "__name__", Value: "thermostat_control_calculated_setpoint_celsius"}),
				Samples: calculatedSetpointSamples,
			},
			prompb.TimeSeries{
				Labels:  append(baseLabels, prompb.Label{Name: "__name__", Value: "thermostat_control_temperature_difference_celsius"}),
				Samples: tempDiffSamples,
			},
			prompb.TimeSeries{
				Labels:  append(baseLabels, prompb.Label{Name: "__name__", Value: "thermostat_control_setpoint_adjustment_celsius"}),
				Samples: setpointAdjSamples,
			},
			prompb.TimeSeries{
				Labels:  append(baseLabels, prompb.Label{Name: "__name__", Value: "thermostat_control_action"}),
				Samples: actionSamples,
			},
		)
	}

	return timeSeries, nil
}

// buildWeightedAvgTimeSeries builds time series for weighted average readings
func (p *Pusher) buildWeightedAvgTimeSeries(readings []*buffer.WeightedAvgReading) ([]prompb.TimeSeries, error) {
	// Group readings by sensor
	type sensorKey struct {
		name string
		id   int
	}
	sensorReadings := make(map[sensorKey][]*buffer.WeightedAvgReading)
	for _, reading := range readings {
		key := sensorKey{name: reading.SensorName, id: reading.SensorID}
		sensorReadings[key] = append(sensorReadings[key], reading)
	}

	// Build time series for each sensor
	var timeSeries []prompb.TimeSeries
	for key, sensorData := range sensorReadings {
		// Create base labels for this sensor
		baseLabels := []prompb.Label{
			{
				Name:  "sensor_name",
				Value: key.name,
			},
			{
				Name:  "sensor_id",
				Value: fmt.Sprintf("%d", key.id),
			},
			{
				Name:  "mac",
				Value: sensorData[0].MAC, // All readings have same MAC
			},
		}

		// Temperature time series (weighted average)
		tempSamples := make([]prompb.Sample, 0, len(sensorData))

		for _, reading := range sensorData {
			// Round timestamp to nearest 10 seconds, then convert to milliseconds
			ts, ok := reading.Timestamp.(time.Time)
			if !ok {
				p.logger.Warn("invalid timestamp type in weighted avg reading",
					zap.String("sensor_name", key.name),
				)
				continue
			}
			roundedTime := roundToTenSeconds(ts)
			timestampMs := roundedTime.UnixMilli()

			// Add temperature sample
			tempSamples = append(tempSamples, prompb.Sample{
				Value:     reading.TemperatureCelsius,
				Timestamp: timestampMs,
			})
		}

		// Add weighted average temperature time series
		tempLabels := append([]prompb.Label{
			{
				Name:  "__name__",
				Value: "ble_temperature_weighted_avg_celsius",
			},
		}, baseLabels...)
		timeSeries = append(timeSeries, prompb.TimeSeries{
			Labels:  tempLabels,
			Samples: tempSamples,
		})
	}

	return timeSeries, nil
}

// roundToTenSeconds rounds a time to the nearest 10-second interval
func roundToTenSeconds(t time.Time) time.Time {
	// Truncate to 10-second boundary
	truncated := t.Truncate(10 * time.Second)

	// Calculate how far we are into the current 10-second interval
	remainder := t.Sub(truncated)

	// If we're at 5 seconds or more, round up to next 10-second mark
	if remainder >= 5*time.Second {
		return truncated.Add(10 * time.Second)
	}

	// Otherwise, round down
	return truncated
}
