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
	"go.uber.org/zap"
)

// Pusher handles pushing metrics to Prometheus remote_write endpoint
type Pusher struct {
	url      string
	username string
	password string
	client   *http.Client
	logger   *zap.Logger
	lastPush time.Time
}

// New creates a new Prometheus pusher
func New(url, username, password string, logger *zap.Logger) *Pusher {
	return &Pusher{
		url:      url,
		username: username,
		password: password,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:   logger,
		lastPush: time.Now(),
	}
}

// Push pushes sensor readings to Prometheus
func (p *Pusher) Push(ctx context.Context, readings []*buffer.SensorReading) error {
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
			p.logger.Info("successfully pushed metrics",
				zap.Int("sensor_count", p.countUniqueSensors(readings)),
				zap.Int("data_points", len(readings)),
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
func (p *Pusher) buildWriteRequest(readings []*buffer.SensorReading) (*prompb.WriteRequest, error) {
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

	return &prompb.WriteRequest{
		Timeseries: timeSeries,
	}, nil
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

// countUniqueSensors counts the number of unique sensor MACs in the readings
func (p *Pusher) countUniqueSensors(readings []*buffer.SensorReading) int {
	sensors := make(map[string]bool)
	for _, reading := range readings {
		sensors[reading.MAC] = true
	}
	return len(sensors)
}

// LastPushTime returns the time of the last successful push
func (p *Pusher) LastPushTime() time.Time {
	return p.lastPush
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
