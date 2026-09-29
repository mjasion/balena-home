package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/jsternberg/zap-logfmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config represents the application configuration
type Config struct {
	BLE           BLEConfig           `yaml:"ble"`
	Power         PowerConfig         `yaml:"power"`
	Pyroscope     PyroscopeConfig     `yaml:"pyroscope"`
	OpenTelemetry OpenTelemetryConfig `yaml:"opentelemetry"`
	Prometheus    PrometheusConfig    `yaml:"prometheus"`
	Logging       LoggingConfig       `yaml:"logging"`
	Aggregator    AggregatorConfig    `yaml:"aggregator"`
	Scheduler     SchedulerConfig     `yaml:"scheduler"`
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

// PowerConfig contains power meter scraping configuration
type PowerConfig struct {
	Enabled              bool    `yaml:"enabled" env:"POWER_ENABLED" env-default:"false"`
	ScrapeURL            string  `yaml:"scrapeUrl" env:"POWER_SCRAPE_URL"`
	ScrapeTimeoutSeconds float64 `yaml:"scrapeTimeoutSeconds" env:"POWER_SCRAPE_TIMEOUT" env-default:"1.5"`
	Cron                 string  `yaml:"cron" env:"POWER_CRON"` // Required: Cron expression with seconds (e.g., "* * * * * *" for every second)
}

// PyroscopeConfig contains Pyroscope profiling configuration
type PyroscopeConfig struct {
	Enabled           bool              `yaml:"enabled" env:"PYROSCOPE_ENABLED" env-default:"false"`
	ServerURL         string            `yaml:"serverUrl" env:"PYROSCOPE_SERVER_URL"`
	ApplicationName   string            `yaml:"applicationName" env:"PYROSCOPE_APPLICATION_NAME" env-default:"home-controller"`
	BasicAuthUser     string            `yaml:"basicAuthUser" env:"PYROSCOPE_BASIC_AUTH_USER"`
	BasicAuthPassword string            `yaml:"basicAuthPassword" env:"PYROSCOPE_BASIC_AUTH_PASSWORD"`
	ProfileTypes      []string          `yaml:"profileTypes"`
	MutexProfileRate  int               `yaml:"mutexProfileRate" env:"PYROSCOPE_MUTEX_PROFILE_RATE" env-default:"0"`
	BlockProfileRate  int               `yaml:"blockProfileRate" env:"PYROSCOPE_BLOCK_PROFILE_RATE" env-default:"0"`
	DisableGCRuns     bool              `yaml:"disableGCRuns" env:"PYROSCOPE_DISABLE_GC_RUNS" env-default:"false"`
	Tags              map[string]string `yaml:"tags"`
}

// OpenTelemetryConfig contains OpenTelemetry tracing configuration
type OpenTelemetryConfig struct {
	Enabled             bool              `yaml:"enabled" env:"OTEL_ENABLED" env-default:"false"`
	Endpoint            string            `yaml:"endpoint" env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	Protocol            string            `yaml:"protocol" env:"OTEL_EXPORTER_OTLP_PROTOCOL" env-default:"http/protobuf"`
	Headers             string            `yaml:"headers" env:"OTEL_EXPORTER_OTLP_HEADERS"` // Format: "key1=value1,key2=value2"
	ServiceName         string            `yaml:"serviceName" env:"OTEL_SERVICE_NAME" env-default:"home-controller"`
	SamplingRate        float64           `yaml:"samplingRate" env:"OTEL_SAMPLING_RATE" env-default:"1.0"`
	MetricsSamplingRate float64           `yaml:"metricsSamplingRate" env:"OTEL_METRICS_SAMPLING_RATE" env-default:"0.1"` // Separate rate for metrics operations
	ResourceAttributes  map[string]string `yaml:"resourceAttributes"`
}

// PrometheusConfig contains Prometheus metrics push configuration
type PrometheusConfig struct {
	PushIntervalSeconds int    `yaml:"pushIntervalSeconds" env:"PUSH_INTERVAL_SECONDS" env-default:"15"`
	URL                 string `yaml:"prometheusUrl" env:"PROMETHEUS_URL,GRAFANA_CLOUD_PROMETHEUS_URL" env-required:"true"`
	Username            string `yaml:"prometheusUsername" env:"PROMETHEUS_USERNAME,GRAFANA_CLOUD_PROMETHEUS_USERNAME" env-required:"true"`
	Password            string `yaml:"prometheusPassword" env:"PROMETHEUS_PASSWORD,GRAFANA_CLOUD_API_KEY"`
	StartAtEvenSecond   bool   `yaml:"startAtEvenSecond" env:"START_AT_EVEN_SECOND" env-default:"true"`
	BufferSize          int    `yaml:"bufferSize" env:"BUFFER_SIZE" env-default:"1000"`
	BatchSize           int    `yaml:"batchSize" env:"BATCH_SIZE" env-default:"1000"`
	Cron                string `yaml:"cron" env:"PROMETHEUS_CRON"` // Optional: Use cron syntax instead of interval (e.g., "*/10 * * * * *" for every 10 seconds with seconds field)
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Format string `yaml:"logFormat" env:"LOG_FORMAT" env-default:"console"`
	Level  string `yaml:"logLevel" env:"LOG_LEVEL" env-default:"info"`
}

// AggregatorConfig contains configuration for the BLE sensor aggregator
type AggregatorConfig struct {
	Enabled bool   `yaml:"enabled" env:"AGGREGATOR_ENABLED" env-default:"true"`
	Cron    string `yaml:"cron" env:"AGGREGATOR_CRON"` // Required: Cron expression with seconds (e.g., "0 * * * * *" for every minute)
}

// SchedulerConfig contains configuration for the job scheduler and UI
type SchedulerConfig struct {
	UIEnabled bool   `yaml:"uiEnabled" env:"SCHEDULER_UI_ENABLED" env-default:"true"`
	UIHost    string `yaml:"uiHost" env:"SCHEDULER_UI_HOST" env-default:"0.0.0.0"`
	UIPort    int    `yaml:"uiPort" env:"SCHEDULER_UI_PORT" env-default:"8080"`
}

var macAddressRegex = regexp.MustCompile(`^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`)
var timeFormatRegex = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

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

	// Validate Power configuration if enabled
	if c.Power.Enabled {
		if c.Power.ScrapeURL == "" {
			return fmt.Errorf("power scrape URL is required when power monitoring is enabled")
		}
		if c.Power.ScrapeTimeoutSeconds <= 0 {
			return fmt.Errorf("power scrape timeout must be positive")
		}
		if c.Power.Cron == "" {
			return fmt.Errorf("power cron expression is required when power monitoring is enabled")
		}
	}

	// Validate Pyroscope configuration if enabled
	if c.Pyroscope.Enabled {
		if c.Pyroscope.ServerURL == "" {
			return fmt.Errorf("pyroscope server URL is required when profiling is enabled")
		}
		if c.Pyroscope.ApplicationName == "" {
			return fmt.Errorf("pyroscope application name is required when profiling is enabled")
		}
		if c.Pyroscope.MutexProfileRate < 0 {
			return fmt.Errorf("pyroscope mutex profile rate must be non-negative")
		}
		if c.Pyroscope.BlockProfileRate < 0 {
			return fmt.Errorf("pyroscope block profile rate must be non-negative")
		}

		// Validate profile types if specified
		if len(c.Pyroscope.ProfileTypes) > 0 {
			validTypes := map[string]bool{
				"cpu":           true,
				"alloc_objects": true,
				"alloc_space":   true,
				"inuse_objects": true,
				"inuse_space":   true,
				"goroutines":    true,
				"mutex":         true,
				"block":         true,
			}
			for _, pt := range c.Pyroscope.ProfileTypes {
				if !validTypes[pt] {
					return fmt.Errorf("invalid pyroscope profile type: %s (must be one of: cpu, alloc_objects, alloc_space, inuse_objects, inuse_space, goroutines, mutex, block)", pt)
				}
			}
		}
	}

	// Validate OpenTelemetry configuration if enabled
	if c.OpenTelemetry.Enabled {
		if c.OpenTelemetry.Endpoint == "" {
			return fmt.Errorf("opentelemetry endpoint is required when tracing is enabled")
		}
		if c.OpenTelemetry.ServiceName == "" {
			return fmt.Errorf("opentelemetry service name is required when tracing is enabled")
		}
		if c.OpenTelemetry.SamplingRate < 0.0 || c.OpenTelemetry.SamplingRate > 1.0 {
			return fmt.Errorf("opentelemetry sampling rate must be between 0.0 and 1.0, got: %.2f", c.OpenTelemetry.SamplingRate)
		}
		if c.OpenTelemetry.MetricsSamplingRate < 0.0 || c.OpenTelemetry.MetricsSamplingRate > 1.0 {
			return fmt.Errorf("opentelemetry metrics sampling rate must be between 0.0 and 1.0, got: %.2f", c.OpenTelemetry.MetricsSamplingRate)
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

	// Validate log format
	c.Logging.Format = strings.ToLower(c.Logging.Format)
	if c.Logging.Format != "console" && c.Logging.Format != "json" && c.Logging.Format != "logfmt" {
		return fmt.Errorf("log format must be 'console', 'json', or 'logfmt', got: %s", c.Logging.Format)
	}

	// Validate log level
	c.Logging.Level = strings.ToLower(c.Logging.Level)
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("log level must be one of: debug, info, warn, error, got: %s", c.Logging.Level)
	}

	// Validate aggregator configuration if enabled
	if c.Aggregator.Enabled {
		if c.Aggregator.Cron == "" {
			return fmt.Errorf("aggregator cron expression is required when aggregator is enabled")
		}
	}

	return nil
}

// InitLogger initializes a zap logger based on the logging configuration
func (c *Config) InitLogger() (*zap.Logger, error) {
	// Parse log level
	var level zapcore.Level
	switch c.Logging.Level {
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

	// Handle logfmt format
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

		return zap.New(core, zap.AddCaller()), nil
	}

	// Create encoder config for json and console
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "ts"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	// Create logger based on format
	var logger *zap.Logger
	if c.Logging.Format == "json" {
		config := zap.Config{
			Level:            zap.NewAtomicLevelAt(level),
			Encoding:         "json",
			EncoderConfig:    encoderConfig,
			OutputPaths:      []string{"stdout"},
			ErrorOutputPaths: []string{"stderr"},
		}
		var err error
		logger, err = config.Build()
		if err != nil {
			return nil, fmt.Errorf("failed to build JSON logger: %w", err)
		}
	} else {
		// Console format
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		config := zap.Config{
			Level:            zap.NewAtomicLevelAt(level),
			Encoding:         "console",
			EncoderConfig:    encoderConfig,
			OutputPaths:      []string{"stdout"},
			ErrorOutputPaths: []string{"stderr"},
		}
		var err error
		logger, err = config.Build()
		if err != nil {
			return nil, fmt.Errorf("failed to build console logger: %w", err)
		}
	}

	return logger, nil
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
		zap.Bool("power_enabled", c.Power.Enabled),
		zap.String("power_scrape_url", c.Power.ScrapeURL),
		zap.Float64("power_scrape_timeout_seconds", c.Power.ScrapeTimeoutSeconds),
		zap.String("power_cron", c.Power.Cron),
		zap.Bool("pyroscope_enabled", c.Pyroscope.Enabled),
		zap.String("pyroscope_server_url", c.Pyroscope.ServerURL),
		zap.String("pyroscope_application_name", c.Pyroscope.ApplicationName),
		zap.Bool("pyroscope_auth_configured", c.Pyroscope.BasicAuthUser != "" && c.Pyroscope.BasicAuthPassword != ""),
		zap.Strings("pyroscope_profile_types", c.Pyroscope.ProfileTypes),
		zap.Bool("opentelemetry_enabled", c.OpenTelemetry.Enabled),
		zap.String("opentelemetry_endpoint", c.OpenTelemetry.Endpoint),
		zap.String("opentelemetry_protocol", c.OpenTelemetry.Protocol),
		zap.String("opentelemetry_service_name", c.OpenTelemetry.ServiceName),
		zap.Bool("opentelemetry_headers_configured", c.OpenTelemetry.Headers != ""),
		zap.Float64("opentelemetry_sampling_rate", c.OpenTelemetry.SamplingRate),
		zap.Int("push_interval_seconds", c.Prometheus.PushIntervalSeconds),
		zap.String("prometheus_url", c.Prometheus.URL),
		zap.String("prometheus_username", c.Prometheus.Username),
		zap.Bool("prometheus_password_set", c.Prometheus.Password != ""),
		zap.Bool("start_at_even_second", c.Prometheus.StartAtEvenSecond),
		zap.Int("buffer_size", c.Prometheus.BufferSize),
		zap.Int("batch_size", c.Prometheus.BatchSize),
		zap.String("log_format", c.Logging.Format),
		zap.String("log_level", c.Logging.Level),
		zap.Bool("aggregator_enabled", c.Aggregator.Enabled),
		zap.String("aggregator_cron", c.Aggregator.Cron),
	)
}
