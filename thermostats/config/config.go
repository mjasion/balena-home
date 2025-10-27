package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ilyakaznacheev/cleanenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config represents the application configuration
type Config struct {
	BLE        BLEConfig        `yaml:"ble"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// BLEConfig contains BLE scanning configuration
type BLEConfig struct {
	ScanIntervalSeconds int            `yaml:"scanIntervalSeconds" env:"SCAN_INTERVAL_SECONDS" env-default:"60"`
	Sensors             []SensorConfig `yaml:"sensors"`
}

// SensorConfig contains configuration for a single sensor
type SensorConfig struct {
	Name       string `yaml:"name"`
	ID         int    `yaml:"id"`
	MACAddress string `yaml:"macAddress"`
}

// PrometheusConfig contains Prometheus metrics push configuration
type PrometheusConfig struct {
	PushIntervalSeconds int    `yaml:"pushIntervalSeconds" env:"PUSH_INTERVAL_SECONDS" env-default:"15"`
	URL                 string `yaml:"prometheusUrl" env:"PROMETHEUS_URL" env-required:"true"`
	Username            string `yaml:"prometheusUsername" env:"PROMETHEUS_USERNAME" env-required:"true"`
	Password            string `yaml:"prometheusPassword" env:"PROMETHEUS_PASSWORD"`
	MetricName          string `yaml:"metricName" env:"METRIC_NAME" env-default:"ble_temperature_celsius"`
	StartAtEvenSecond   bool   `yaml:"startAtEvenSecond" env:"START_AT_EVEN_SECOND" env-default:"true"`
	BufferSize          int    `yaml:"bufferSize" env:"BUFFER_SIZE" env-default:"1000"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Format string `yaml:"logFormat" env:"LOG_FORMAT" env-default:"console"`
	Level  string `yaml:"logLevel" env:"LOG_LEVEL" env-default:"info"`
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

	// Validate Prometheus URL
	if c.Prometheus.URL == "" {
		return fmt.Errorf("prometheus URL is required")
	}

	if c.Prometheus.Username == "" {
		return fmt.Errorf("prometheus username is required")
	}

	// Validate scan interval
	if c.BLE.ScanIntervalSeconds < 1 {
		return fmt.Errorf("scan interval must be at least 1 second")
	}

	// Validate push interval
	if c.Prometheus.PushIntervalSeconds < 1 {
		return fmt.Errorf("push interval must be at least 1 second")
	}

	// Validate buffer size
	if c.Prometheus.BufferSize < 1 {
		return fmt.Errorf("buffer size must be at least 1")
	}

	// Validate log format
	c.Logging.Format = strings.ToLower(c.Logging.Format)
	if c.Logging.Format != "console" && c.Logging.Format != "json" {
		return fmt.Errorf("log format must be 'console' or 'json', got: %s", c.Logging.Format)
	}

	// Validate log level
	c.Logging.Level = strings.ToLower(c.Logging.Level)
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("log level must be one of: debug, info, warn, error, got: %s", c.Logging.Level)
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

	// Create encoder config
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
		zap.Int("scan_interval_seconds", c.BLE.ScanIntervalSeconds),
		zap.Int("sensor_count", len(c.BLE.Sensors)),
		zap.Strings("sensors", sensorInfo),
		zap.Int("push_interval_seconds", c.Prometheus.PushIntervalSeconds),
		zap.String("prometheus_url", c.Prometheus.URL),
		zap.String("prometheus_username", c.Prometheus.Username),
		zap.Bool("prometheus_password_set", c.Prometheus.Password != ""),
		zap.String("metric_name", c.Prometheus.MetricName),
		zap.Bool("start_at_even_second", c.Prometheus.StartAtEvenSecond),
		zap.Int("buffer_size", c.Prometheus.BufferSize),
		zap.String("log_format", c.Logging.Format),
		zap.String("log_level", c.Logging.Level),
	)
}
