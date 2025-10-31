package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ilyakaznacheev/cleanenv"
	pkgconfig "github.com/mjasion/balena-home/pkg/config"
	"go.uber.org/zap"
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
	Logging pkgconfig.LoggingConfig `yaml:"logging"`

	// OpenTelemetry configuration
	OpenTelemetry pkgconfig.OpenTelemetryConfig `yaml:"opentelemetry"`

	// Profiling configuration
	Profiling pkgconfig.ProfilingConfig `yaml:"profiling"`
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

	// Validate logging configuration
	if err := pkgconfig.ValidateLogging(&c.Logging); err != nil {
		return fmt.Errorf("logging validation failed: %w", err)
	}

	// Validate OpenTelemetry configuration
	if err := pkgconfig.ValidateOpenTelemetry(&c.OpenTelemetry); err != nil {
		return fmt.Errorf("opentelemetry validation failed: %w", err)
	}

	// Validate Profiling configuration
	if err := pkgconfig.ValidateProfiling(&c.Profiling); err != nil {
		return fmt.Errorf("profiling validation failed: %w", err)
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
	return pkgconfig.NewLogger(&c.Logging)
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
