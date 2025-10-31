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
	BLE        BLEConfig        `yaml:"ble"`
	Netatmo    NetatmoConfig    `yaml:"netatmo"`
	Power      PowerConfig      `yaml:"power"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
	Logging    LoggingConfig    `yaml:"logging"`
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

// PowerConfig contains power meter scraping configuration
type PowerConfig struct {
	Enabled               bool    `yaml:"enabled" env:"POWER_ENABLED" env-default:"false"`
	ScrapeURL             string  `yaml:"scrapeUrl" env:"POWER_SCRAPE_URL"`
	ScrapeIntervalSeconds int     `yaml:"scrapeIntervalSeconds" env:"POWER_SCRAPE_INTERVAL" env-default:"2"`
	ScrapeTimeoutSeconds  float64 `yaml:"scrapeTimeoutSeconds" env:"POWER_SCRAPE_TIMEOUT" env-default:"1.5"`
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

	// Validate Power configuration if enabled
	if c.Power.Enabled {
		if c.Power.ScrapeURL == "" {
			return fmt.Errorf("power scrape URL is required when power monitoring is enabled")
		}
		if c.Power.ScrapeIntervalSeconds < 1 {
			return fmt.Errorf("power scrape interval must be at least 1 second")
		}
		if c.Power.ScrapeTimeoutSeconds <= 0 {
			return fmt.Errorf("power scrape timeout must be positive")
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

		return zap.New(core), nil
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
		zap.Bool("netatmo_enabled", c.Netatmo.Enabled),
		zap.Bool("netatmo_configured", c.Netatmo.ClientID != "" && c.Netatmo.RefreshToken != ""),
		zap.Int("netatmo_fetch_interval_seconds", c.Netatmo.FetchInterval),
		zap.Bool("power_enabled", c.Power.Enabled),
		zap.String("power_scrape_url", c.Power.ScrapeURL),
		zap.Int("power_scrape_interval_seconds", c.Power.ScrapeIntervalSeconds),
		zap.Float64("power_scrape_timeout_seconds", c.Power.ScrapeTimeoutSeconds),
		zap.Int("push_interval_seconds", c.Prometheus.PushIntervalSeconds),
		zap.String("prometheus_url", c.Prometheus.URL),
		zap.String("prometheus_username", c.Prometheus.Username),
		zap.Bool("prometheus_password_set", c.Prometheus.Password != ""),
		zap.Bool("start_at_even_second", c.Prometheus.StartAtEvenSecond),
		zap.Int("buffer_size", c.Prometheus.BufferSize),
		zap.Int("batch_size", c.Prometheus.BatchSize),
		zap.String("log_format", c.Logging.Format),
		zap.String("log_level", c.Logging.Level),
	)
}
