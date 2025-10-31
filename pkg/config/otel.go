package config

import (
	"fmt"
	"os"
)

// OpenTelemetryConfig contains OpenTelemetry configuration
type OpenTelemetryConfig struct {
	Enabled            bool                  `yaml:"enabled" env:"OTEL_ENABLED" env-default:"false"`
	ServiceName        string                `yaml:"serviceName" env:"OTEL_SERVICE_NAME"`
	ServiceVersion     string                `yaml:"serviceVersion" env:"OTEL_SERVICE_VERSION" env-default:"1.0.0"`
	Environment        string                `yaml:"environment" env:"OTEL_ENVIRONMENT" env-default:"production"`
	Protocol           string                `yaml:"protocol" env:"OTEL_EXPORTER_OTLP_PROTOCOL" env-default:"http/protobuf"`
	Endpoint           string                `yaml:"endpoint" env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	Headers            map[string]string     `yaml:"headers"`
	Traces             OTelTracesConfig      `yaml:"traces"`
	Metrics            OTelMetricsConfig     `yaml:"metrics"`
	ResourceAttributes map[string]string     `yaml:"resourceAttributes"`
}

// OTelTracesConfig contains OpenTelemetry traces configuration
type OTelTracesConfig struct {
	Enabled       bool              `yaml:"enabled" env:"OTEL_TRACES_ENABLED" env-default:"true"`
	Endpoint      string            `yaml:"endpoint" env:"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"`
	Headers       map[string]string `yaml:"headers"`
	SamplingRatio float64           `yaml:"samplingRatio" env:"OTEL_TRACES_SAMPLING_RATIO" env-default:"1.0"`
	Batch         OTelBatchConfig   `yaml:"batch"`
}

// OTelMetricsConfig contains OpenTelemetry metrics configuration
type OTelMetricsConfig struct {
	Enabled              bool              `yaml:"enabled" env:"OTEL_METRICS_ENABLED" env-default:"true"`
	Endpoint             string            `yaml:"endpoint" env:"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"`
	Headers              map[string]string `yaml:"headers"`
	IntervalMillis       int               `yaml:"intervalMillis" env:"OTEL_METRICS_INTERVAL" env-default:"30000"`
	EnableRuntimeMetrics bool              `yaml:"enableRuntimeMetrics" env:"OTEL_ENABLE_RUNTIME_METRICS" env-default:"true"`
}

// OTelBatchConfig contains batch processor configuration for traces
type OTelBatchConfig struct {
	ScheduleDelayMillis int `yaml:"scheduleDelayMillis" env:"OTEL_BSP_SCHEDULE_DELAY" env-default:"5000"`
	MaxQueueSize        int `yaml:"maxQueueSize" env:"OTEL_BSP_MAX_QUEUE_SIZE" env-default:"2048"`
	MaxExportBatchSize  int `yaml:"maxExportBatchSize" env:"OTEL_BSP_MAX_EXPORT_BATCH_SIZE" env-default:"512"`
}

// ValidateOpenTelemetry validates OpenTelemetry configuration if enabled
func ValidateOpenTelemetry(cfg *OpenTelemetryConfig) error {
	if !cfg.Enabled {
		return nil
	}

	// Validate service name
	if cfg.ServiceName == "" {
		return fmt.Errorf("opentelemetry service name is required when OpenTelemetry is enabled")
	}

	// Validate protocol
	if cfg.Protocol != "" && cfg.Protocol != "http/protobuf" && cfg.Protocol != "grpc" {
		return fmt.Errorf("opentelemetry protocol must be 'http/protobuf' or 'grpc', got: %s", cfg.Protocol)
	}

	// Validate traces configuration
	if cfg.Traces.Enabled {
		if cfg.Traces.Endpoint == "" {
			// Check environment variable fallback (specific takes precedence over general)
			if os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" &&
			   cfg.Endpoint == "" &&
			   os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
				return fmt.Errorf("opentelemetry traces endpoint is required when traces are enabled")
			}
		}

		// Validate sampling ratio
		if cfg.Traces.SamplingRatio < 0 || cfg.Traces.SamplingRatio > 1 {
			return fmt.Errorf("opentelemetry traces sampling ratio must be between 0 and 1, got: %f", cfg.Traces.SamplingRatio)
		}

		// Validate batch configuration
		if cfg.Traces.Batch.ScheduleDelayMillis < 0 {
			return fmt.Errorf("opentelemetry traces batch schedule delay must be >= 0")
		}
		if cfg.Traces.Batch.MaxQueueSize < 1 {
			return fmt.Errorf("opentelemetry traces batch max queue size must be >= 1")
		}
		if cfg.Traces.Batch.MaxExportBatchSize < 1 {
			return fmt.Errorf("opentelemetry traces batch max export batch size must be >= 1")
		}
	}

	// Validate metrics configuration
	if cfg.Metrics.Enabled {
		if cfg.Metrics.Endpoint == "" {
			// Check environment variable fallback (specific takes precedence over general)
			if os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") == "" &&
			   cfg.Endpoint == "" &&
			   os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
				return fmt.Errorf("opentelemetry metrics endpoint is required when metrics are enabled")
			}
		}

		// Validate interval
		if cfg.Metrics.IntervalMillis < 1000 {
			return fmt.Errorf("opentelemetry metrics interval must be at least 1000ms (1 second)")
		}
	}

	return nil
}
