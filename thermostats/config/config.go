package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ilyakaznacheev/cleanenv"
	pkgconfig "github.com/mjasion/balena-home/pkg/config"
	"go.uber.org/zap"
)

// Config represents the application configuration
type Config struct {
	BLE           BLEConfig                       `yaml:"ble"`
	Netatmo       NetatmoConfig                   `yaml:"netatmo"`
	Prometheus    PrometheusConfig                `yaml:"prometheus"`
	OpenTelemetry pkgconfig.OpenTelemetryConfig   `yaml:"opentelemetry"`
	Logging       pkgconfig.LoggingConfig         `yaml:"logging"`
}

// BLEConfig contains BLE scanning configuration
type BLEConfig struct {
	Sensors []SensorConfig `yaml:"sensors"`
}

// SensorConfig contains configuration for a single sensor
type SensorConfig struct {
	Name       string `yaml:"name"`
	ID         int    `yaml:"id"`
	MACAddress string `yaml:"macAddress"`
}

// NetatmoConfig contains Netatmo API configuration
type NetatmoConfig struct {
	Enabled       bool   `yaml:"enabled" env:"NETATMO_ENABLED" env-default:"false"`
	ClientID      string `yaml:"clientId" env:"NETATMO_CLIENT_ID"`
	ClientSecret  string `yaml:"clientSecret" env:"NETATMO_CLIENT_SECRET"`
	RefreshToken  string `yaml:"refreshToken" env:"NETATMO_REFRESH_TOKEN"`
	FetchInterval int    `yaml:"fetchIntervalSeconds" env:"NETATMO_FETCH_INTERVAL" env-default:"60"`
}

// PrometheusConfig contains Prometheus metrics push configuration
type PrometheusConfig struct {
	PushIntervalSeconds int    `yaml:"pushIntervalSeconds" env:"PUSH_INTERVAL_SECONDS" env-default:"15"`
	URL                 string `yaml:"prometheusUrl" env:"PROMETHEUS_URL" env-required:"true"`
	Username            string `yaml:"prometheusUsername" env:"PROMETHEUS_USERNAME" env-required:"true"`
	Password            string `yaml:"prometheusPassword" env:"PROMETHEUS_PASSWORD"`
	StartAtEvenSecond   bool   `yaml:"startAtEvenSecond" env:"START_AT_EVEN_SECOND" env-default:"true"`
	BufferSize          int    `yaml:"bufferSize" env:"BUFFER_SIZE" env-default:"1000"`
	BatchSize           int    `yaml:"batchSize" env:"BATCH_SIZE" env-default:"1000"`
}

var macAddressRegex = regexp.MustCompile(`^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`)

// Load loads configuration from a YAML file with environment variable overrides
func Load(configPath string) (*Config, error) {
	var cfg Config

	if err := cleanenv.ReadConfig(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate sensor MAC addresses
	if len(c.BLE.Sensors) == 0 {
		return fmt.Errorf("at least one sensor must be configured")
	}

	// Track unique IDs and MACs
	seenIDs := make(map[int]bool)
	seenMACs := make(map[string]bool)

	for i, sensor := range c.BLE.Sensors {
		// Validate name
		if sensor.Name == "" {
			return fmt.Errorf("sensor %d: name is required", i)
		}

		// Validate ID
		if sensor.ID < 1 {
			return fmt.Errorf("sensor %s: ID must be >= 1, got %d", sensor.Name, sensor.ID)
		}
		if seenIDs[sensor.ID] {
			return fmt.Errorf("sensor %s: duplicate ID %d", sensor.Name, sensor.ID)
		}
		seenIDs[sensor.ID] = true

		// Validate MAC address
		if !macAddressRegex.MatchString(sensor.MACAddress) {
			return fmt.Errorf("sensor %s: invalid MAC address format: %s (expected format: XX:XX:XX:XX:XX:XX)", sensor.Name, sensor.MACAddress)
		}
		macUpper := strings.ToUpper(sensor.MACAddress)
		if seenMACs[macUpper] {
			return fmt.Errorf("sensor %s: duplicate MAC address %s", sensor.Name, sensor.MACAddress)
		}
		seenMACs[macUpper] = true
	}

	// Validate Netatmo configuration if enabled
	if c.Netatmo.Enabled {
		if c.Netatmo.ClientID == "" {
			return fmt.Errorf("netatmo client ID is required when Netatmo is enabled")
		}
		if c.Netatmo.ClientSecret == "" {
			return fmt.Errorf("netatmo client secret is required when Netatmo is enabled")
		}
		if c.Netatmo.RefreshToken == "" {
			return fmt.Errorf("netatmo refresh token is required when Netatmo is enabled")
		}
		if c.Netatmo.FetchInterval < 1 {
			return fmt.Errorf("netatmo fetch interval must be at least 1 second")
		}
	}

	// Validate Prometheus URL
	if c.Prometheus.URL == "" {
		return fmt.Errorf("prometheus URL is required")
	}

	if c.Prometheus.Username == "" {
		return fmt.Errorf("prometheus username is required")
	}

	// Validate push interval
	if c.Prometheus.PushIntervalSeconds < 1 {
		return fmt.Errorf("push interval must be at least 1 second")
	}

	// Validate buffer size
	if c.Prometheus.BufferSize < 1 {
		return fmt.Errorf("buffer size must be at least 1")
	}

	// Validate batch size
	if c.Prometheus.BatchSize < 1 {
		return fmt.Errorf("batch size must be at least 1")
	}

	// Validate logging configuration
	if err := pkgconfig.ValidateLogging(&c.Logging); err != nil {
		return fmt.Errorf("logging validation failed: %w", err)
	}

	// Validate OpenTelemetry configuration
	if err := pkgconfig.ValidateOpenTelemetry(&c.OpenTelemetry); err != nil {
		return fmt.Errorf("opentelemetry validation failed: %w", err)
	}

	return nil
}

// NewLogger creates a zap logger based on the logging configuration
func (c *Config) NewLogger() (*zap.Logger, error) {
	return pkgconfig.NewLogger(&c.Logging)
}

// Redacted returns a copy of the config with sensitive fields redacted for logging
func (c *Config) Redacted() map[string]interface{} {
	// Build sensor info for redacted output
	sensorInfo := make([]map[string]interface{}, len(c.BLE.Sensors))
	for i, sensor := range c.BLE.Sensors {
		sensorInfo[i] = map[string]interface{}{
			"name":       sensor.Name,
			"id":         sensor.ID,
			"macAddress": sensor.MACAddress,
		}
	}

	return map[string]interface{}{
		"ble": map[string]interface{}{
			"sensors": sensorInfo,
		},
		"netatmo": map[string]interface{}{
			"enabled":                 c.Netatmo.Enabled,
			"clientIdSet":             c.Netatmo.ClientID != "",
			"clientSecretSet":         c.Netatmo.ClientSecret != "",
			"refreshTokenSet":         c.Netatmo.RefreshToken != "",
			"fetchIntervalSeconds":    c.Netatmo.FetchInterval,
		},
		"prometheus": map[string]interface{}{
			"pushIntervalSeconds": c.Prometheus.PushIntervalSeconds,
			"prometheusUrl":       c.Prometheus.URL,
			"prometheusUsername":  c.Prometheus.Username,
			"prometheusPassword":  "***",
			"startAtEvenSecond":   c.Prometheus.StartAtEvenSecond,
			"bufferSize":          c.Prometheus.BufferSize,
			"batchSize":           c.Prometheus.BatchSize,
		},
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

// PrintConfig prints the configuration (masking sensitive fields)
func (c *Config) PrintConfig(logger *zap.Logger) {
	// Build sensor info for logging
	sensorInfo := make([]string, len(c.BLE.Sensors))
	for i, sensor := range c.BLE.Sensors {
		sensorInfo[i] = fmt.Sprintf("%s (ID:%d, MAC:%s)", sensor.Name, sensor.ID, sensor.MACAddress)
	}

	logger.Info("configuration loaded",
		zap.Int("sensor_count", len(c.BLE.Sensors)),
		zap.Strings("sensors", sensorInfo),
		zap.Bool("netatmo_enabled", c.Netatmo.Enabled),
		zap.Bool("netatmo_configured", c.Netatmo.ClientID != "" && c.Netatmo.RefreshToken != ""),
		zap.Int("netatmo_fetch_interval_seconds", c.Netatmo.FetchInterval),
		zap.Int("push_interval_seconds", c.Prometheus.PushIntervalSeconds),
		zap.String("prometheus_url", c.Prometheus.URL),
		zap.String("prometheus_username", c.Prometheus.Username),
		zap.Bool("prometheus_password_set", c.Prometheus.Password != ""),
		zap.Bool("start_at_even_second", c.Prometheus.StartAtEvenSecond),
		zap.Int("buffer_size", c.Prometheus.BufferSize),
		zap.Int("batch_size", c.Prometheus.BatchSize),
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
