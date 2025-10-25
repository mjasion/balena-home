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
	"github.com/mjasion/balena-home/pstryk_metric/scraper"
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
	lastPush   time.Time
	logger     *zap.Logger
}

// New creates a new Pusher instance
func New(url, username, password, metricName string, logger *zap.Logger) *Pusher {
	return &Pusher{
		url:        url,
		username:   username,
		password:   password,
		metricName: metricName,
		logger:     logger,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Push sends buffered metrics to Prometheus using remote_write protocol
func (p *Pusher) Push(ctx context.Context, results []*scraper.ScrapeResult) error {
	if len(results) == 0 {
		p.logger.Debug("No metrics to push, skipping")
		return nil
	}

	// Collect all readings from each sensor (grouped by sensor ID)
	sensorReadings := make(map[int][]scraper.ActivePowerReading)

	for _, result := range results {
		if result.Error != nil {
			continue
		}
		for _, reading := range result.Readings {
			// Append all readings for each sensor
			sensorReadings[reading.SensorID] = append(sensorReadings[reading.SensorID], reading)
		}
	}

	if len(sensorReadings) == 0 {
		p.logger.Debug("No valid readings to push")
		return nil
	}

	// Build Prometheus remote_write request
	writeRequest := p.buildWriteRequest(sensorReadings)

	// Count total data points (samples) being pushed and find timestamp range
	totalSamples := 0
	var minTime, maxTime time.Time
	for _, ts := range writeRequest.Timeseries {
		totalSamples += len(ts.Samples)
		for _, sample := range ts.Samples {
			sampleTime := time.UnixMilli(sample.Timestamp)
			if minTime.IsZero() || sampleTime.Before(minTime) {
				minTime = sampleTime
			}
			if maxTime.IsZero() || sampleTime.After(maxTime) {
				maxTime = sampleTime
			}
		}
	}

	// Try up to 3 times with exponential backoff
	var lastErr error
	backoff := time.Second

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
			p.logger.Info("Retrying push",
				zap.Int("attempt", attempt+1),
				zap.Int("maxAttempts", 3),
				zap.Int("dataPoints", totalSamples))
		}

		err := p.pushOnce(ctx, writeRequest)
		if err == nil {
			p.lastPush = time.Now()
			p.logger.Info("Successfully pushed metrics",
				zap.Int("sensors", len(sensorReadings)),
				zap.Int("dataPoints", totalSamples),
				zap.Time("timeRangeStart", minTime),
				zap.Time("timeRangeEnd", maxTime),
				zap.Duration("timeSpan", maxTime.Sub(minTime)))
			return nil
		}

		lastErr = err
		p.logger.Warn("Push attempt failed",
			zap.Int("attempt", attempt+1),
			zap.Error(err))
	}

	return fmt.Errorf("all push retry attempts exhausted: %w", lastErr)
}

// buildWriteRequest creates a Prometheus remote_write request from sensor readings
func (p *Pusher) buildWriteRequest(sensorReadings map[int][]scraper.ActivePowerReading) *prompb.WriteRequest {
	timeseries := make([]prompb.TimeSeries, 0, len(sensorReadings))

	for sensorID, readings := range sensorReadings {
		// Create samples for all readings of this sensor
		samples := make([]prompb.Sample, 0, len(readings))
		for _, reading := range readings {
			samples = append(samples, prompb.Sample{
				Value:     reading.Value,
				Timestamp: reading.Timestamp.UnixMilli(),
			})
		}

		ts := prompb.TimeSeries{
			Labels: []prompb.Label{
				{Name: "__name__", Value: p.metricName},
				{Name: "sensor_id", Value: fmt.Sprintf("%d", sensorID)},
			},
			Samples: samples,
		}
		timeseries = append(timeseries, ts)
	}

	return &prompb.WriteRequest{
		Timeseries: timeseries,
	}
}

// pushOnce performs a single push attempt using Prometheus remote_write protocol
func (p *Pusher) pushOnce(ctx context.Context, writeRequest *prompb.WriteRequest) error {
	// Marshal to protobuf
	data, err := proto.Marshal(writeRequest)
	if err != nil {
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}

	// Compress with snappy
	compressed := snappy.Encode(nil, data)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set required headers for Prometheus remote_write
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	req.SetBasicAuth(p.username, p.password)

	// Send request
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("push failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetLastPushTime returns the timestamp of the last successful push
func (p *Pusher) GetLastPushTime() time.Time {
	return p.lastPush
}
