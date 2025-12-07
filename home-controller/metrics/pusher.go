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
	"github.com/prometheus/prometheus/prompb"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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
	tracer       trace.Tracer
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
		tracer:       otel.Tracer("home-controller/metrics"),
		batchSize:    batchSize,
	}
}

// Run executes a single push iteration (called by scheduler)
func (p *Pusher) Run(ctx context.Context) {
	p.pushMetrics()
}

// pushMetrics gets all readings from buffer and pushes them in batches
func (p *Pusher) pushMetrics() {

	ctx, span := p.tracer.Start(context.Background(), "prometheus_pusher_job",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("job", "prometheus_pusher"),
			attribute.String("operation", "push_metrics_batch"),
		),
	)
	defer span.End()

	// Get all readings and clear buffer atomically
	readings := p.buffer.GetAllAndClear(ctx)
	if len(readings) == 0 {
		p.logger.Debug("no readings to push",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
		)
		span.SetStatus(codes.Ok, "no readings to push")
		return
	}

	// Count readings by type
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

	span.SetAttributes(
		attribute.Int("total_readings", len(readings)),
		attribute.Int("ble_readings", bleCount),
		attribute.Int("netatmo_readings", netatmoCount),
		attribute.Int("power_readings", powerCount),
		attribute.Int("control_readings", controlCount),
		attribute.Int("weighted_avg_readings", weightedAvgCount),
		attribute.Int("batch_size", p.batchSize),
	)

	p.logger.Debug("pushing metrics to prometheus",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("total_readings", len(readings)),
		zap.Int("ble_readings", bleCount),
		zap.Int("netatmo_readings", netatmoCount),
		zap.Int("power_readings", powerCount),
		zap.Int("control_readings", controlCount),
		zap.Int("weighted_avg_readings", weightedAvgCount),
		zap.Int("batch_size", p.batchSize),
	)

	// Process readings in batches
	totalBatches := (len(readings) + p.batchSize - 1) / p.batchSize

	for batchNum := 0; batchNum < totalBatches; batchNum++ {
		start := batchNum * p.batchSize
		end := start + p.batchSize
		if end > len(readings) {
			end = len(readings)
		}
		batch := readings[start:end]

		span.AddEvent(fmt.Sprintf("pushing_batch_%d", batchNum+1), trace.WithAttributes(
			attribute.Int("batch_number", batchNum+1),
			attribute.Int("batch_readings", len(batch)),
		))

		p.logger.Debug("pushing batch",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Int("batch_number", batchNum+1),
			zap.Int("total_batches", totalBatches),
			zap.Int("batch_readings", len(batch)),
		)

		err := p.Push(ctx, batch)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, fmt.Sprintf("failed to push batch %d", batchNum+1))
			p.logger.Error("failed to push batch, re-adding remaining readings to buffer",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
				zap.Error(err),
				zap.Int("batch_number", batchNum+1),
				zap.Int("failed_readings", len(readings)-start),
			)
			// Re-add the failed batch and all remaining batches
			p.buffer.AddMultiple(readings[start:])
			return
		}

		p.logger.Debug("successfully pushed batch",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Int("batch_number", batchNum+1),
			zap.Int("batch_readings", len(batch)),
		)
	}

	span.SetAttributes(attribute.Int("total_batches", totalBatches))
	span.SetStatus(codes.Ok, "all batches pushed successfully")
}

// Push pushes sensor readings to Prometheus
func (p *Pusher) Push(ctx context.Context, readings []*buffer.Reading) error {
	ctx, span := p.tracer.Start(ctx, "metrics.Push",
		trace.WithAttributes(
			attribute.Int("readings_count", len(readings)),
		),
	)
	defer span.End()

	if len(readings) == 0 {
		p.logger.Debug("no readings to push",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
		)
		span.SetStatus(codes.Ok, "no readings to push")
		return nil
	}

	// Count readings by type
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

	span.SetAttributes(
		attribute.Int("ble_data_points", bleCount),
		attribute.Int("netatmo_data_points", netatmoCount),
		attribute.Int("power_data_points", powerCount),
		attribute.Int("control_data_points", controlCount),
		attribute.Int("weighted_avg_data_points", weightedAvgCount),
	)

	// Build write request
	writeReq, err := p.buildWriteRequest(readings)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to build write request")
		return fmt.Errorf("failed to build write request: %w", err)
	}

	// Count time series and samples
	totalTimeSeries := len(writeReq.Timeseries)
	totalSamples := 0
	metricNames := make(map[string]int)
	for _, ts := range writeReq.Timeseries {
		totalSamples += len(ts.Samples)
		// Extract metric name from labels
		for _, label := range ts.Labels {
			if label.Name == "__name__" {
				metricNames[label.Value]++
				break
			}
		}
	}

	span.SetAttributes(
		attribute.Int("time_series_count", totalTimeSeries),
		attribute.Int("total_samples", totalSamples),
		attribute.Int("unique_metric_names", len(metricNames)),
	)

	// Log metric names and their counts
	p.logger.Debug("pushing metrics to Prometheus",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("time_series_count", totalTimeSeries),
		zap.Int("total_samples", totalSamples),
		zap.Any("metric_names", metricNames),
	)

	// Try to push with retries
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		span.AddEvent(fmt.Sprintf("push_attempt_%d", attempt))

		err := p.pushOnce(ctx, writeReq)
		if err == nil {
			p.lastPush = time.Now()

			p.logger.Info("successfully pushed metrics",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
				zap.Int("ble_data_points", bleCount),
				zap.Int("netatmo_data_points", netatmoCount),
				zap.Int("power_data_points", powerCount),
				zap.Int("control_data_points", controlCount),
				zap.Int("weighted_avg_data_points", weightedAvgCount),
				zap.Int("total_data_points", len(readings)),
				zap.Int("time_series_count", totalTimeSeries),
				zap.Int("total_samples", totalSamples),
				zap.Int("attempt", attempt),
			)

			span.SetAttributes(attribute.Int("successful_attempt", attempt))
			span.SetStatus(codes.Ok, "metrics pushed successfully")
			return nil
		}

		lastErr = err
		span.AddEvent("push_attempt_failed", trace.WithAttributes(
			attribute.Int("attempt", attempt),
			attribute.String("error", err.Error()),
		))

		p.logger.Warn("failed to push metrics, will retry",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Int("attempt", attempt),
			zap.Error(err),
		)

		// Exponential backoff: 1s, 2s, 4s
		if attempt < 3 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				span.RecordError(ctx.Err())
				span.SetStatus(codes.Error, "context cancelled during retry")
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	span.RecordError(lastErr)
	span.SetStatus(codes.Error, "failed to push metrics after 3 attempts")
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
		key := sensorKey{name: reading.RoomName, id: reading.SensorID}
		sensorReadings[key] = append(sensorReadings[key], reading)
	}

	// Build time series for each sensor and metric
	var timeSeries []prompb.TimeSeries
	for key, sensorData := range sensorReadings {
		// Create base labels for this sensor
		baseLabels := []prompb.Label{
			{
				Name:  "room_name",
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
					zap.String("room_name", key.name),
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
	ctx, span := p.tracer.Start(ctx, "metrics.pushOnce",
		trace.WithAttributes(
			attribute.String("prometheus_url", p.url),
		),
	)
	defer span.End()

	// Marshal to protobuf
	data, err := proto.Marshal(writeReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to marshal protobuf")
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}

	span.SetAttributes(attribute.Int("protobuf_size_bytes", len(data)))

	// Compress with snappy
	compressed := snappy.Encode(nil, data)

	compressionRatio := float64(len(data)) / float64(len(compressed))
	span.SetAttributes(
		attribute.Int("compressed_size_bytes", len(compressed)),
		attribute.Float64("compression_ratio", compressionRatio),
	)

	p.logger.Debug("prepared metrics payload",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("protobuf_size_bytes", len(data)),
		zap.Int("compressed_size_bytes", len(compressed)),
		zap.Float64("compression_ratio", compressionRatio),
	)

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewReader(compressed))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create request")
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
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to send request")
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	// Check response
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		p.logger.Debug("Prometheus remote_write error response",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", string(body)),
		)
		span.SetStatus(codes.Error, fmt.Sprintf("non-2xx status code: %d", resp.StatusCode))
		return fmt.Errorf("received non-2xx status code: %d, body: %s", resp.StatusCode, string(body))
	}

	p.logger.Debug("Prometheus remote_write successful",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("status_code", resp.StatusCode),
	)

	span.SetStatus(codes.Ok, "metrics pushed successfully")
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
		if len(sensorData) > 0 && sensorData[0].RoomName != "" {
			labels = append(labels, prompb.Label{
				Name:  "room_name",
				Value: sensorData[0].RoomName,
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
		key := sensorKey{name: reading.RoomName, id: reading.SensorID}
		sensorReadings[key] = append(sensorReadings[key], reading)
	}

	// Build time series for each sensor
	var timeSeries []prompb.TimeSeries
	for key, sensorData := range sensorReadings {
		// Create base labels for this sensor
		baseLabels := []prompb.Label{
			{
				Name:  "room_name",
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
