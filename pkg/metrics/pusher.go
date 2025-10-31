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
	"github.com/mjasion/balena-home/pkg/buffer"
	"github.com/mjasion/balena-home/pkg/types"
	"github.com/prometheus/prometheus/prompb"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// TimeSeriesBuilder is a function that converts readings to Prometheus time series
type TimeSeriesBuilder func(ctx context.Context, readings []*types.Reading) ([]prompb.TimeSeries, error)

// Pusher handles pushing metrics to Prometheus remote_write endpoint
type Pusher struct {
	url          string
	username     string
	password     string
	client       *http.Client
	logger       *zap.Logger
	lastPush     time.Time
	buffer       *buffer.RingBuffer[*types.Reading]
	pushInterval time.Duration
	batchSize    int
	tsBuilder    TimeSeriesBuilder
}

// Config contains configuration for the Prometheus pusher
type Config struct {
	URL               string
	Username          string
	Password          string
	PushIntervalSec   int
	BatchSize         int
	TimeSeriesBuilder TimeSeriesBuilder
}

// New creates a new Prometheus pusher with OpenTelemetry instrumentation
func New(cfg Config, buf *buffer.RingBuffer[*types.Reading], logger *zap.Logger) *Pusher {
	// Create HTTP client with OpenTelemetry instrumentation
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
				return "prometheus.remote_write"
			}),
		),
	}

	return &Pusher{
		url:          cfg.URL,
		username:     cfg.Username,
		password:     cfg.Password,
		client:       httpClient,
		logger:       logger,
		lastPush:     time.Now(),
		buffer:       buf,
		pushInterval: time.Duration(cfg.PushIntervalSec) * time.Second,
		batchSize:    cfg.BatchSize,
		tsBuilder:    cfg.TimeSeriesBuilder,
	}
}

// Start begins the periodic metrics pushing in a goroutine
func (p *Pusher) Start(ctx context.Context) {
	ticker := time.NewTicker(p.pushInterval)
	defer ticker.Stop()

	p.logger.Info("prometheus pusher started",
		zap.Duration("push_interval", p.pushInterval),
		zap.Int("batch_size", p.batchSize),
	)

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("prometheus pusher stopping")
			return
		case <-ticker.C:
			// Get all readings and clear buffer atomically
			readings := p.buffer.GetAllAndClear()
			if len(readings) == 0 {
				p.logger.Debug("no readings to push")
				continue
			}

			p.logger.Debug("pushing metrics to prometheus",
				zap.Int("total_readings", len(readings)),
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
					// Re-add the failed batch and all remaining batches to buffer
					for _, reading := range readings[start:] {
						p.buffer.Add(reading)
					}
					break
				}

				p.logger.Debug("successfully pushed batch",
					zap.Int("batch_number", batchNum+1),
					zap.Int("batch_readings", len(batch)),
				)
			}
		}
	}
}

// Push pushes readings to Prometheus with retry logic
func (p *Pusher) Push(ctx context.Context, readings []*types.Reading) error {
	tracer := otel.Tracer("metrics")
	ctx, span := tracer.Start(ctx, "metrics.Push",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.Int("metrics.total_readings", len(readings)),
		),
	)
	defer span.End()

	if len(readings) == 0 {
		p.logger.Debug("no readings to push")
		span.SetStatus(codes.Ok, "no readings to push")
		return nil
	}

	// Count reading types
	typeCounts := make(map[types.ReadingType]int)
	for _, r := range readings {
		typeCounts[r.Type]++
	}

	// Add type counts as span attributes
	for typ, count := range typeCounts {
		span.SetAttributes(
			attribute.Int(fmt.Sprintf("metrics.%s_readings", typ), count),
		)
	}

	// Build write request using provided builder
	writeReq, err := p.buildWriteRequest(ctx, readings)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to build write request")
		return fmt.Errorf("failed to build write request: %w", err)
	}

	span.AddEvent("write request built",
		trace.WithAttributes(
			attribute.Int("metrics.time_series_count", len(writeReq.Timeseries)),
		),
	)

	// Try to push with retries (3 attempts with exponential backoff)
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		span.AddEvent("push attempt",
			trace.WithAttributes(
				attribute.Int("metrics.attempt", attempt),
			),
		)

		err := p.pushOnce(ctx, writeReq)
		if err == nil {
			p.lastPush = time.Now()

			logFields := []zap.Field{
				zap.Int("total_data_points", len(readings)),
				zap.Int("attempt", attempt),
			}
			for typ, count := range typeCounts {
				logFields = append(logFields, zap.Int(string(typ)+"_data_points", count))
			}

			p.logger.Info("successfully pushed metrics", logFields...)

			span.SetAttributes(
				attribute.Int("metrics.successful_attempt", attempt),
			)
			span.SetStatus(codes.Ok, "metrics pushed successfully")
			return nil
		}

		lastErr = err
		p.logger.Warn("failed to push metrics, will retry",
			zap.Int("attempt", attempt),
			zap.Error(err),
		)

		span.AddEvent("push attempt failed",
			trace.WithAttributes(
				attribute.Int("metrics.attempt", attempt),
				attribute.String("error", err.Error()),
			),
		)

		// Exponential backoff: 1s, 2s, 4s
		if attempt < 3 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				span.RecordError(ctx.Err())
				span.SetStatus(codes.Error, "context cancelled")
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	span.RecordError(lastErr)
	span.SetStatus(codes.Error, "failed after 3 attempts")
	return fmt.Errorf("failed to push metrics after 3 attempts: %w", lastErr)
}

// buildWriteRequest converts readings to Prometheus WriteRequest using the configured builder
func (p *Pusher) buildWriteRequest(ctx context.Context, readings []*types.Reading) (*prompb.WriteRequest, error) {
	tracer := otel.Tracer("metrics")
	ctx, span := tracer.Start(ctx, "metrics.buildWriteRequest")
	defer span.End()

	if p.tsBuilder == nil {
		err := fmt.Errorf("no TimeSeriesBuilder configured")
		span.RecordError(err)
		span.SetStatus(codes.Error, "no builder configured")
		return nil, err
	}

	timeSeries, err := p.tsBuilder(ctx, readings)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "builder failed")
		return nil, fmt.Errorf("time series builder failed: %w", err)
	}

	span.SetAttributes(
		attribute.Int("metrics.total_time_series", len(timeSeries)),
	)
	span.SetStatus(codes.Ok, "write request built successfully")

	return &prompb.WriteRequest{
		Timeseries: timeSeries,
	}, nil
}

// pushOnce performs a single push attempt to Prometheus
func (p *Pusher) pushOnce(ctx context.Context, writeReq *prompb.WriteRequest) error {
	tracer := otel.Tracer("metrics")
	ctx, span := tracer.Start(ctx, "metrics.pushOnce")
	defer span.End()

	// Serialize protobuf
	data, err := proto.Marshal(writeReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to marshal protobuf")
		return fmt.Errorf("failed to marshal protobuf: %w", err)
	}

	span.SetAttributes(
		attribute.Int("metrics.protobuf_size_bytes", len(data)),
	)

	// Compress with snappy
	compressed := snappy.Encode(nil, data)
	span.SetAttributes(
		attribute.Int("metrics.compressed_size_bytes", len(compressed)),
		attribute.Float64("metrics.compression_ratio", float64(len(data))/float64(len(compressed))),
	)

	// Create HTTP request
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

	// Add basic auth if configured
	if p.username != "" && p.password != "" {
		req.SetBasicAuth(p.username, p.password)
	}

	// Send request
	resp, err := p.client.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "request failed")
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	span.SetAttributes(
		attribute.Int("http.status_code", resp.StatusCode),
	)

	// Read response body for error details
	body, _ := io.ReadAll(resp.Body)

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("received non-2xx status code: %d, body: %s", resp.StatusCode, string(body))
		span.RecordError(err)
		span.SetStatus(codes.Error, "non-2xx response")
		return err
	}

	span.SetStatus(codes.Ok, "push successful")
	return nil
}

// LastPushTime returns the time of the last successful push
func (p *Pusher) LastPushTime() time.Time {
	return p.lastPush
}
