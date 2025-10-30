package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/jsternberg/zap-logfmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config holds all configuration parameters for the energy meter scraper
type Config struct {
	// Scraping configuration
	ScrapeURL             string  `yaml:"scrapeUrl" env:"SCRAPE_URL" env-required:"true"`
	ScrapeIntervalSeconds int     `yaml:"scrapeIntervalSeconds" env:"SCRAPE_INTERVAL_SECONDS" env-default:"2"`
	ScrapeTimeoutSeconds  float64 `yaml:"scrapeTimeoutSeconds" env:"SCRAPE_TIMEOUT_SECONDS" env-default:"1.5"`

	// Push configuration
	PushIntervalSeconds int `yaml:"pushIntervalSeconds" env:"PUSH_INTERVAL_SECONDS" env-default:"15"`

	// Prometheus configuration
	PrometheusURL      string `yaml:"prometheusUrl" env:"PROMETHEUS_URL" env-required:"true"`
	PrometheusUsername string `yaml:"prometheusUsername" env:"PROMETHEUS_USERNAME" env-required:"true"`
	PrometheusPassword string `yaml:"prometheusPassword" env:"PROMETHEUS_PASSWORD" env-required:"true"`

	// Metric configuration
	MetricName        string `yaml:"metricName" env:"METRIC_NAME" env-default:"active_power_watts"`
	StartAtEvenSecond bool   `yaml:"startAtEvenSecond" env:"START_AT_EVEN_SECOND" env-default:"true"`

	// Buffer configuration
	BufferSize int `yaml:"bufferSize" env:"BUFFER_SIZE" env-default:"1000"`

	// Health check configuration
	HealthCheckPort int `yaml:"healthCheckPort" env:"HEALTH_CHECK_PORT" env-default:"8080"`

	// Logging configuration
	Logging LoggingConfig `yaml:"logging"`

	// OpenTelemetry configuration
	OpenTelemetry OpenTelemetryConfig `yaml:"opentelemetry"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Format string `yaml:"logFormat" env:"LOG_FORMAT" env-default:"console"`
	Level  string `yaml:"logLevel" env:"LOG_LEVEL" env-default:"info"`
}

// OpenTelemetryConfig contains OpenTelemetry configuration
type OpenTelemetryConfig struct {
	Enabled            bool                  `yaml:"enabled" env:"OTEL_ENABLED" env-default:"false"`
	ServiceName        string                `yaml:"serviceName" env:"OTEL_SERVICE_NAME" env-default:"pstryk-metric"`
	ServiceVersion     string                `yaml:"serviceVersion" env:"OTEL_SERVICE_VERSION" env-default:"1.0.0"`
	Environment        string                `yaml:"environment" env:"OTEL_ENVIRONMENT" env-default:"production"`
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

// Load reads configuration from the specified file path and applies environment variable overrides
func Load(configPath string) (*Config, error) {
	var cfg Config

	if err := cleanenv.ReadConfig(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("failed to read config from %s: %w", configPath, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// Validate checks that all configuration parameters are valid
func (c *Config) Validate() error {
	// Validate scrape URL
	if _, err := url.ParseRequestURI(c.ScrapeURL); err != nil {
		return fmt.Errorf("invalid scrapeUrl: %w", err)
	}

	// Validate Prometheus URL
	if _, err := url.ParseRequestURI(c.PrometheusURL); err != nil {
		return fmt.Errorf("invalid prometheusUrl: %w", err)
	}

	// Validate intervals are positive
	if c.ScrapeIntervalSeconds <= 0 {
		return fmt.Errorf("scrapeIntervalSeconds must be positive, got %d", c.ScrapeIntervalSeconds)
	}

	if c.ScrapeTimeoutSeconds <= 0 {
		return fmt.Errorf("scrapeTimeoutSeconds must be positive, got %f", c.ScrapeTimeoutSeconds)
	}

	if c.PushIntervalSeconds <= 0 {
		return fmt.Errorf("pushIntervalSeconds must be positive, got %d", c.PushIntervalSeconds)
	}

	// Validate buffer size
	if c.BufferSize <= 0 {
		return fmt.Errorf("bufferSize must be positive, got %d", c.BufferSize)
	}

	// Validate health check port
	if c.HealthCheckPort <= 0 || c.HealthCheckPort > 65535 {
		return fmt.Errorf("healthCheckPort must be between 1 and 65535, got %d", c.HealthCheckPort)
	}

	// Validate metric name is not empty
	if strings.TrimSpace(c.MetricName) == "" {
		return fmt.Errorf("metricName cannot be empty")
	}

	// Validate log format
	c.Logging.Format = strings.ToLower(c.Logging.Format)
	if c.Logging.Format != "json" && c.Logging.Format != "console" && c.Logging.Format != "logfmt" {
		return fmt.Errorf("logFormat must be 'json', 'console', or 'logfmt', got '%s'", c.Logging.Format)
	}

	// Validate log level
	c.Logging.Level = strings.ToLower(c.Logging.Level)
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("logLevel must be one of: debug, info, warn, error, got '%s'", c.Logging.Level)
	}

	// Validate OpenTelemetry configuration if enabled
	if c.OpenTelemetry.Enabled {
		// Validate service name
		if c.OpenTelemetry.ServiceName == "" {
			return fmt.Errorf("opentelemetry service name is required when OpenTelemetry is enabled")
		}

		// Validate traces configuration
		if c.OpenTelemetry.Traces.Enabled {
			if c.OpenTelemetry.Traces.Endpoint == "" {
				// Check environment variable fallback
				if os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
					return fmt.Errorf("opentelemetry traces endpoint is required when traces are enabled")
				}
			}

			// Validate sampling ratio
			if c.OpenTelemetry.Traces.SamplingRatio < 0 || c.OpenTelemetry.Traces.SamplingRatio > 1 {
				return fmt.Errorf("opentelemetry traces sampling ratio must be between 0 and 1, got: %f", c.OpenTelemetry.Traces.SamplingRatio)
			}

			// Validate batch configuration
			if c.OpenTelemetry.Traces.Batch.ScheduleDelayMillis < 0 {
				return fmt.Errorf("opentelemetry traces batch schedule delay must be >= 0")
			}
			if c.OpenTelemetry.Traces.Batch.MaxQueueSize < 1 {
				return fmt.Errorf("opentelemetry traces batch max queue size must be >= 1")
			}
			if c.OpenTelemetry.Traces.Batch.MaxExportBatchSize < 1 {
				return fmt.Errorf("opentelemetry traces batch max export batch size must be >= 1")
			}
		}

		// Validate metrics configuration
		if c.OpenTelemetry.Metrics.Enabled {
			if c.OpenTelemetry.Metrics.Endpoint == "" {
				// Check environment variable fallback
				if os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
					return fmt.Errorf("opentelemetry metrics endpoint is required when metrics are enabled")
				}
			}

			// Validate interval
			if c.OpenTelemetry.Metrics.IntervalMillis < 1000 {
				return fmt.Errorf("opentelemetry metrics interval must be at least 1000ms (1 second)")
			}
		}
	}

	return nil
}

// Redacted returns a copy of the config with sensitive fields redacted for logging
func (c *Config) Redacted() map[string]interface{} {
	return map[string]interface{}{
		"scrapeUrl":             c.ScrapeURL,
		"scrapeIntervalSeconds": c.ScrapeIntervalSeconds,
		"scrapeTimeoutSeconds":  c.ScrapeTimeoutSeconds,
		"pushIntervalSeconds":   c.PushIntervalSeconds,
		"prometheusUrl":         redactURL(c.PrometheusURL),
		"prometheusUsername":    c.PrometheusUsername,
		"prometheusPassword":    "***",
		"metricName":            c.MetricName,
		"startAtEvenSecond":     c.StartAtEvenSecond,
		"bufferSize":            c.BufferSize,
		"healthCheckPort":       c.HealthCheckPort,
		"logging": map[string]interface{}{
			"logFormat": c.Logging.Format,
			"logLevel":  c.Logging.Level,
		},
		"opentelemetry": map[string]interface{}{
			"enabled":        c.OpenTelemetry.Enabled,
			"serviceName":    c.OpenTelemetry.ServiceName,
			"serviceVersion": c.OpenTelemetry.ServiceVersion,
			"environment":    c.OpenTelemetry.Environment,
			"traces": map[string]interface{}{
				"enabled":       c.OpenTelemetry.Traces.Enabled,
				"endpointSet":   c.OpenTelemetry.Traces.Endpoint != "",
				"samplingRatio": c.OpenTelemetry.Traces.SamplingRatio,
			},
			"metrics": map[string]interface{}{
				"enabled":              c.OpenTelemetry.Metrics.Enabled,
				"endpointSet":          c.OpenTelemetry.Metrics.Endpoint != "",
				"intervalMillis":       c.OpenTelemetry.Metrics.IntervalMillis,
				"enableRuntimeMetrics": c.OpenTelemetry.Metrics.EnableRuntimeMetrics,
			},
		},
	}
}

// NewLogger creates a zap logger based on the configuration
func (c *Config) NewLogger() (*zap.Logger, error) {
	// Set log level
	var level zapcore.Level
	switch strings.ToLower(c.Logging.Level) {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	default:
		level = zapcore.InfoLevel
	}

	// Handle logfmt format separately
	if c.Logging.Format == "logfmt" {
		encoderConfig := zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}

		core := zapcore.NewCore(
			zaplogfmt.NewEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			level,
		)

		return zap.New(core), nil
	}

	// Handle json and console formats
	var zapConfig zap.Config
	if c.Logging.Format == "json" {
		zapConfig = zap.NewProductionConfig()
	} else {
		zapConfig = zap.NewDevelopmentConfig()
	}

	zapConfig.Level = zap.NewAtomicLevelAt(level)

	return zapConfig.Build()
}

// PrintConfig prints the configuration (masking sensitive fields)
func (c *Config) PrintConfig(logger *zap.Logger) {
	logger.Info("configuration loaded",
		zap.String("scrape_url", c.ScrapeURL),
		zap.Int("scrape_interval_seconds", c.ScrapeIntervalSeconds),
		zap.Float64("scrape_timeout_seconds", c.ScrapeTimeoutSeconds),
		zap.Int("push_interval_seconds", c.PushIntervalSeconds),
		zap.String("prometheus_url", c.PrometheusURL),
		zap.String("prometheus_username", c.PrometheusUsername),
		zap.Bool("prometheus_password_set", c.PrometheusPassword != ""),
		zap.String("metric_name", c.MetricName),
		zap.Bool("start_at_even_second", c.StartAtEvenSecond),
		zap.Int("buffer_size", c.BufferSize),
		zap.Int("health_check_port", c.HealthCheckPort),
		zap.Bool("otel_enabled", c.OpenTelemetry.Enabled),
		zap.String("otel_service_name", c.OpenTelemetry.ServiceName),
		zap.String("otel_service_version", c.OpenTelemetry.ServiceVersion),
		zap.String("otel_environment", c.OpenTelemetry.Environment),
		zap.Bool("otel_traces_enabled", c.OpenTelemetry.Traces.Enabled),
		zap.Bool("otel_traces_endpoint_set", c.OpenTelemetry.Traces.Endpoint != ""),
		zap.Float64("otel_traces_sampling_ratio", c.OpenTelemetry.Traces.SamplingRatio),
		zap.Bool("otel_metrics_enabled", c.OpenTelemetry.Metrics.Enabled),
		zap.Bool("otel_metrics_endpoint_set", c.OpenTelemetry.Metrics.Endpoint != ""),
		zap.Int("otel_metrics_interval_ms", c.OpenTelemetry.Metrics.IntervalMillis),
		zap.Bool("otel_runtime_metrics_enabled", c.OpenTelemetry.Metrics.EnableRuntimeMetrics),
		zap.String("log_format", c.Logging.Format),
		zap.String("log_level", c.Logging.Level),
	)
}

// redactURL removes credentials from URLs for logging
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "***"
	}
	if u.User != nil {
		u.User = url.UserPassword("***", "***")
	}
	return u.String()
}
