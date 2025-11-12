package otel

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.uber.org/zap"
)

// InitTracer initializes the OpenTelemetry tracer with OTLP HTTP exporter
func InitTracer(ctx context.Context, cfg *config.OpenTelemetryConfig, logger *zap.Logger) (func(context.Context) error, error) {
	if !cfg.Enabled {
		logger.Info("OpenTelemetry tracing is disabled")
		return func(ctx context.Context) error { return nil }, nil
	}

	logger.Info("initializing OpenTelemetry tracer",
		zap.String("endpoint", cfg.Endpoint),
		zap.String("protocol", cfg.Protocol),
		zap.String("service_name", cfg.ServiceName),
		zap.Float64("sampling_rate", cfg.SamplingRate),
	)

	// Parse endpoint to extract host and path
	endpoint := cfg.Endpoint
	var urlPath string

	// Remove protocol prefix if present
	if len(endpoint) > 8 && endpoint[:8] == "https://" {
		endpoint = endpoint[8:]
	} else if len(endpoint) > 7 && endpoint[:7] == "http://" {
		endpoint = endpoint[7:]
	}

	// Extract path if present (e.g., "/otlp" from "host/otlp")
	if idx := strings.Index(endpoint, "/"); idx > 0 {
		urlPath = endpoint[idx:] // e.g., "/otlp"
		endpoint = endpoint[:idx]  // e.g., "host"
	}

	// Combine extracted path with /v1/traces
	// If urlPath is "/otlp", result will be "/otlp/v1/traces"
	// If urlPath is empty, result will be "/v1/traces"
	fullPath := urlPath + "/v1/traces"

	// Configure OTLP HTTP exporter options
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithURLPath(fullPath),
	}

	logger.Debug("parsed OTLP endpoint",
		zap.String("host", endpoint),
		zap.String("url_path", fullPath),
	)

	// Parse and add headers if configured
	if cfg.Headers != "" {
		headers := parseHeaders(cfg.Headers)
		opts = append(opts, otlptracehttp.WithHeaders(headers))
		logger.Debug("OpenTelemetry headers configured", zap.Int("header_count", len(headers)))
	}

	// Use HTTPS if endpoint starts with https://
	if len(cfg.Endpoint) > 8 && cfg.Endpoint[:8] == "https://" {
		opts = append(opts, otlptracehttp.WithTLSClientConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	} else {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	// Create OTLP HTTP exporter
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP HTTP exporter: %w", err)
	}

	// Build resource attributes
	resourceAttrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion("1.0.0"), // TODO: make this configurable or get from build info
	}

	// Add custom resource attributes
	for key, value := range cfg.ResourceAttributes {
		resourceAttrs = append(resourceAttrs, attribute.String(key, value))
		logger.Debug("custom resource attribute",
			zap.String("key", key),
			zap.String("value", value),
		)
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(resourceAttrs...),
		resource.WithProcessRuntimeDescription(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create sampler with separate metrics sampling rate
	var sampler sdktrace.Sampler
	if cfg.MetricsSamplingRate != cfg.SamplingRate {
		// Use custom sampler with different rate for metrics operations
		sampler = ParentBasedMetricsSampler(cfg.SamplingRate, cfg.MetricsSamplingRate)
		logger.Debug("using ParentBasedMetricsSampler",
			zap.Float64("default_sampling_rate", cfg.SamplingRate),
			zap.Float64("metrics_sampling_rate", cfg.MetricsSamplingRate),
		)
	} else if cfg.SamplingRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
		logger.Debug("using AlwaysSample sampler")
	} else if cfg.SamplingRate <= 0.0 {
		sampler = sdktrace.NeverSample()
		logger.Debug("using NeverSample sampler")
	} else {
		sampler = sdktrace.TraceIDRatioBased(cfg.SamplingRate)
		logger.Debug("using TraceIDRatioBased sampler",
			zap.Float64("sampling_rate", cfg.SamplingRate),
		)
	}

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Set global trace provider
	otel.SetTracerProvider(tp)

	// Set global propagator for context propagation
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Info("OpenTelemetry tracer initialized successfully",
		zap.String("service_name", cfg.ServiceName),
	)

	// Return shutdown function
	shutdown := func(ctx context.Context) error {
		logger.Info("shutting down OpenTelemetry tracer")
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		if err := tp.Shutdown(ctx); err != nil {
			logger.Error("failed to shutdown trace provider", zap.Error(err))
			return fmt.Errorf("failed to shutdown trace provider: %w", err)
		}

		logger.Info("OpenTelemetry tracer shutdown complete")
		return nil
	}

	return shutdown, nil
}

// parseHeaders parses headers from OTEL_EXPORTER_OTLP_HEADERS format
// Format: "key1=value1,key2=value2"
func parseHeaders(headersStr string) map[string]string {
	headers := make(map[string]string)
	for _, pair := range strings.Split(headersStr, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			headers[key] = value
		}
	}
	return headers
}

// basicAuth encodes username and password for HTTP Basic Authentication
func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}
