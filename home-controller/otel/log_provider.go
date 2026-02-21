package otel

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/mjasion/balena-home/thermostats/config"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.uber.org/zap"
)

// InitLogProvider initializes the OpenTelemetry log provider with OTLP HTTP exporter.
// It returns the LoggerProvider (for use with otelzap bridge), a shutdown function, and an error.
// When OTel is disabled, it returns nil provider and a no-op shutdown.
func InitLogProvider(ctx context.Context, cfg *config.OpenTelemetryConfig, res *resource.Resource, logger *zap.Logger) (*log.LoggerProvider, func(context.Context) error, error) {
	noopShutdown := func(ctx context.Context) error { return nil }

	if !cfg.Enabled {
		logger.Info("OpenTelemetry log provider is disabled")
		return nil, noopShutdown, nil
	}

	logger.Info("initializing OpenTelemetry log provider",
		zap.String("endpoint", cfg.Endpoint),
	)

	endpoint, _ := parseEndpoint(cfg.Endpoint)

	opts := []otlploghttp.Option{
		otlploghttp.WithEndpoint(endpoint),
	}

	if cfg.Headers != "" {
		headers := parseHeaders(cfg.Headers)
		opts = append(opts, otlploghttp.WithHeaders(headers))
	}

	if isHTTPS(cfg.Endpoint) {
		opts = append(opts, otlploghttp.WithTLSClientConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	} else {
		opts = append(opts, otlploghttp.WithInsecure())
	}

	exporter, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create OTLP HTTP log exporter: %w", err)
	}

	lp := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(exporter,
			log.WithExportTimeout(10*time.Second),
		)),
		log.WithResource(res),
	)

	logger.Info("OpenTelemetry log provider initialized successfully")

	shutdown := func(ctx context.Context) error {
		logger.Info("shutting down OpenTelemetry log provider")
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		if err := lp.Shutdown(ctx); err != nil {
			logger.Error("failed to shutdown log provider", zap.Error(err))
			return fmt.Errorf("failed to shutdown log provider: %w", err)
		}

		logger.Info("OpenTelemetry log provider shutdown complete")
		return nil
	}

	return lp, shutdown, nil
}
