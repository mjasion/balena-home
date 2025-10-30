package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.uber.org/zap"

	"github.com/mjasion/balena-home/pkg/config"
)

// Providers holds the initialized OpenTelemetry providers
type Providers struct {
	TracerProvider *trace.TracerProvider
	MeterProvider  *metric.MeterProvider
	logger         *zap.Logger
}

// InitProviders initializes OpenTelemetry tracer and meter providers
func InitProviders(ctx context.Context, otelCfg *config.OpenTelemetryConfig, logger *zap.Logger) (*Providers, error) {
	if !otelCfg.Enabled {
		logger.Info("OpenTelemetry is disabled")
		return nil, nil
	}

	logger.Info("initializing OpenTelemetry providers")

	// Create resource with service information
	res, err := newResource(otelCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	providers := &Providers{
		logger: logger,
	}

	// Initialize tracer provider if traces are enabled
	if otelCfg.Traces.Enabled {
		tp, err := newTracerProvider(ctx, otelCfg, res)
		if err != nil {
			return nil, fmt.Errorf("failed to create tracer provider: %w", err)
		}
		providers.TracerProvider = tp
		otel.SetTracerProvider(tp)

		// Set global propagator for context propagation
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))

		logger.Info("tracer provider initialized",
			zap.String("endpoint", getTracesEndpoint(otelCfg)),
			zap.Float64("sampling_ratio", otelCfg.Traces.SamplingRatio),
		)
	}

	// Initialize meter provider if metrics are enabled
	if otelCfg.Metrics.Enabled {
		mp, err := newMeterProvider(ctx, otelCfg, res)
		if err != nil {
			// Clean up tracer provider if meter provider fails
			if providers.TracerProvider != nil {
				_ = providers.TracerProvider.Shutdown(ctx)
			}
			return nil, fmt.Errorf("failed to create meter provider: %w", err)
		}
		providers.MeterProvider = mp
		otel.SetMeterProvider(mp)

		logger.Info("meter provider initialized",
			zap.String("endpoint", getMetricsEndpoint(otelCfg)),
			zap.Int("interval_ms", otelCfg.Metrics.IntervalMillis),
		)

		// Start runtime metrics collection if enabled
		if otelCfg.Metrics.EnableRuntimeMetrics {
			if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second)); err != nil {
				logger.Warn("failed to start runtime metrics collection", zap.Error(err))
			} else {
				logger.Info("runtime metrics collection started")
			}
		}
	}

	logger.Info("OpenTelemetry providers initialized successfully")
	return providers, nil
}

// Shutdown gracefully shuts down the OpenTelemetry providers
func (p *Providers) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}

	p.logger.Info("shutting down OpenTelemetry providers")

	var errs []error

	// Shutdown tracer provider
	if p.TracerProvider != nil {
		if err := p.TracerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer provider shutdown: %w", err))
			p.logger.Error("failed to shutdown tracer provider", zap.Error(err))
		} else {
			p.logger.Info("tracer provider shutdown successfully")
		}
	}

	// Shutdown meter provider
	if p.MeterProvider != nil {
		if err := p.MeterProvider.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider shutdown: %w", err))
			p.logger.Error("failed to shutdown meter provider", zap.Error(err))
		} else {
			p.logger.Info("meter provider shutdown successfully")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during shutdown: %v", errs)
	}

	p.logger.Info("OpenTelemetry providers shutdown complete")
	return nil
}

// newResource creates an OpenTelemetry resource with service information
func newResource(otelCfg *config.OpenTelemetryConfig) (*resource.Resource, error) {
	// Start with standard service attributes
	attributes := []attribute.KeyValue{
		semconv.ServiceNameKey.String(otelCfg.ServiceName),
		semconv.ServiceVersionKey.String(otelCfg.ServiceVersion),
		attribute.String("deployment.environment", otelCfg.Environment),
	}

	// Add custom resource attributes from config
	for key, value := range otelCfg.ResourceAttributes {
		attributes = append(attributes, attribute.String(key, value))
	}

	// Add hostname if available
	if hostname, err := os.Hostname(); err == nil {
		attributes = append(attributes, semconv.HostNameKey.String(hostname))
	}

	return resource.NewWithAttributes(
		semconv.SchemaURL,
		attributes...,
	), nil
}

// newTracerProvider creates a new tracer provider with OTLP exporter
func newTracerProvider(ctx context.Context, otelCfg *config.OpenTelemetryConfig, res *resource.Resource) (*trace.TracerProvider, error) {
	// Build OTLP HTTP exporter options
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(getTracesEndpoint(otelCfg)),
	}

	// Check if endpoint uses HTTPS (default) or HTTP
	if endpoint := getTracesEndpoint(otelCfg); endpoint != "" {
		// Use HTTP if localhost or explicitly set
		if len(endpoint) > 9 && endpoint[:10] == "localhost:" {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
	}

	// Add authentication headers
	headers := getTracesHeaders(otelCfg)
	if len(headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}

	// Create OTLP trace exporter
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	// Configure batch span processor
	batchOpts := []trace.BatchSpanProcessorOption{
		trace.WithMaxQueueSize(otelCfg.Traces.Batch.MaxQueueSize),
		trace.WithMaxExportBatchSize(otelCfg.Traces.Batch.MaxExportBatchSize),
		trace.WithBatchTimeout(time.Duration(otelCfg.Traces.Batch.ScheduleDelayMillis) * time.Millisecond),
	}

	bsp := trace.NewBatchSpanProcessor(exporter, batchOpts...)

	// Create tracer provider with sampling
	tp := trace.NewTracerProvider(
		trace.WithSampler(trace.TraceIDRatioBased(otelCfg.Traces.SamplingRatio)),
		trace.WithResource(res),
		trace.WithSpanProcessor(bsp),
	)

	return tp, nil
}

// newMeterProvider creates a new meter provider with OTLP exporter
func newMeterProvider(ctx context.Context, otelCfg *config.OpenTelemetryConfig, res *resource.Resource) (*metric.MeterProvider, error) {
	// Build OTLP HTTP exporter options
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(getMetricsEndpoint(otelCfg)),
	}

	// Check if endpoint uses HTTPS (default) or HTTP
	if endpoint := getMetricsEndpoint(otelCfg); endpoint != "" {
		// Use HTTP if localhost or explicitly set
		if len(endpoint) > 9 && endpoint[:10] == "localhost:" {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
	}

	// Add authentication headers
	headers := getMetricsHeaders(otelCfg)
	if len(headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(headers))
	}

	// Create OTLP metric exporter
	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP metric exporter: %w", err)
	}

	// Configure periodic reader
	reader := metric.NewPeriodicReader(
		exporter,
		metric.WithInterval(time.Duration(otelCfg.Metrics.IntervalMillis)*time.Millisecond),
	)

	// Create meter provider
	mp := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(reader),
	)

	return mp, nil
}

// getTracesEndpoint returns the traces endpoint with fallback to environment variable
func getTracesEndpoint(otelCfg *config.OpenTelemetryConfig) string {
	if otelCfg.Traces.Endpoint != "" {
		return otelCfg.Traces.Endpoint
	}
	// Check specific traces endpoint env var
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	// Fallback to general OTLP endpoint env var
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
}

// getMetricsEndpoint returns the metrics endpoint with fallback to environment variable
func getMetricsEndpoint(otelCfg *config.OpenTelemetryConfig) string {
	if otelCfg.Metrics.Endpoint != "" {
		return otelCfg.Metrics.Endpoint
	}
	// Check specific metrics endpoint env var
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	// Fallback to general OTLP endpoint env var
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
}

// getTracesHeaders returns the traces headers with fallback to environment variable
func getTracesHeaders(otelCfg *config.OpenTelemetryConfig) map[string]string {
	if len(otelCfg.Traces.Headers) > 0 {
		return otelCfg.Traces.Headers
	}
	// Parse headers from environment variable (format: key1=value1,key2=value2)
	if headersEnv := os.Getenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS"); headersEnv != "" {
		return parseHeadersEnv(headersEnv)
	}
	if headersEnv := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); headersEnv != "" {
		return parseHeadersEnv(headersEnv)
	}
	return nil
}

// getMetricsHeaders returns the metrics headers with fallback to environment variable
func getMetricsHeaders(otelCfg *config.OpenTelemetryConfig) map[string]string {
	if len(otelCfg.Metrics.Headers) > 0 {
		return otelCfg.Metrics.Headers
	}
	// Parse headers from environment variable
	if headersEnv := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS"); headersEnv != "" {
		return parseHeadersEnv(headersEnv)
	}
	if headersEnv := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); headersEnv != "" {
		return parseHeadersEnv(headersEnv)
	}
	return nil
}

// parseHeadersEnv parses headers from environment variable string
// Format: "key1=value1,key2=value2"
func parseHeadersEnv(headersEnv string) map[string]string {
	headers := make(map[string]string)
	if headersEnv == "" {
		return headers
	}

	// Simple parsing - for production use, consider more robust parsing
	pairs := splitHeaderPairs(headersEnv)
	for _, pair := range pairs {
		if idx := findFirstEqual(pair); idx != -1 {
			key := pair[:idx]
			value := pair[idx+1:]
			headers[key] = value
		}
	}

	return headers
}

// splitHeaderPairs splits the header string by commas
func splitHeaderPairs(s string) []string {
	var pairs []string
	current := ""
	for _, c := range s {
		if c == ',' {
			if current != "" {
				pairs = append(pairs, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		pairs = append(pairs, current)
	}
	return pairs
}

// findFirstEqual finds the first '=' in a string
func findFirstEqual(s string) int {
	for i, c := range s {
		if c == '=' {
			return i
		}
	}
	return -1
}
