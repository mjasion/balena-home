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
	url        string
	username   string
	password   string
	metricName string
	client     *http.Client
	logger     *zap.Logger
	lastPush   time.Time
}

// New creates a new Prometheus pusher
func New(url, username, password, metricName string, logger *zap.Logger) *Pusher {
	return &Pusher{
		url:        url,
		username:   username,
		password:   password,
		metricName: metricName,
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
	// Group readings by sensor MAC
	sensorReadings := make(map[string][]*buffer.SensorReading)
	for _, reading := range readings {
		sensorReadings[reading.MAC] = append(sensorReadings[reading.MAC], reading)
	}

	// Build time series for each sensor
	var timeSeries []prompb.TimeSeries
	for mac, sensorData := range sensorReadings {
		// Create samples from readings
		samples := make([]prompb.Sample, len(sensorData))
		for i, reading := range sensorData {
			// Round timestamp to nearest second, then convert to milliseconds
			ts, ok := reading.Timestamp.(time.Time)
			if !ok {
				p.logger.Warn("invalid timestamp type in reading",
					zap.String("mac", mac),
				)
				continue
			}
			roundedTime := ts.Round(time.Second)
			timestampMs := roundedTime.UnixMilli()

			samples[i] = prompb.Sample{
				Value:     reading.TemperatureCelsius,
				Timestamp: timestampMs,
			}
		}

		// Create labels
		labels := []prompb.Label{
			{
				Name:  "__name__",
				Value: p.metricName,
			},
			{
				Name:  "sensor_id",
				Value: mac,
			},
		}

		timeSeries = append(timeSeries, prompb.TimeSeries{
			Labels:  labels,
			Samples: samples,
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

// roundToSecond rounds a time to the nearest second
func roundToSecond(t time.Time) time.Time {
	nsec := t.Nanosecond()
	if nsec >= 500000000 {
		// Round up
		return t.Add(time.Second - time.Duration(nsec)).Truncate(time.Second)
	}
	// Round down
	return t.Truncate(time.Second)
}
