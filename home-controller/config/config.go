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
	BLE               BLEConfig               `yaml:"ble"`
	Netatmo           NetatmoConfig           `yaml:"netatmo"`
	Power             PowerConfig             `yaml:"power"`
	Pyroscope         PyroscopeConfig         `yaml:"pyroscope"`
	Prometheus        PrometheusConfig        `yaml:"prometheus"`
	Logging           LoggingConfig           `yaml:"logging"`
	ThermostatControl ThermostatControlConfig `yaml:"thermostatControl"`
	Aggregator        AggregatorConfig        `yaml:"aggregator"`
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

// ThermostatControlConfig contains thermostat control configuration
type ThermostatControlConfig struct {
	Enabled                         bool                  `yaml:"enabled" env:"THERMOSTAT_CONTROL_ENABLED" env-default:"false"`
	DryRun                          bool                  `yaml:"dryRun" env:"THERMOSTAT_CONTROL_DRY_RUN" env-default:"false"`
	TemperatureThreshold            float64               `yaml:"temperatureThreshold" env:"TEMPERATURE_THRESHOLD" env-default:"0.5"`
	ControlIntervalSeconds          int                   `yaml:"controlIntervalSeconds" env:"CONTROL_INTERVAL_SECONDS" env-default:"60"`
	OverrideDurationMinutes         int                   `yaml:"overrideDurationMinutes" env:"OVERRIDE_DURATION_MINUTES" env-default:"10"`
	RecheckDelayMinutes             int                   `yaml:"recheckDelayMinutes" env:"RECHECK_DELAY_MINUTES" env-default:"5"`
	ExternalModificationResetMinutes int                   `yaml:"externalModificationResetMinutes" env:"EXTERNAL_MODIFICATION_RESET_MINUTES" env-default:"5"`
	MinSetpointCelsius              float64               `yaml:"minSetpointCelsius" env:"MIN_SETPOINT_CELSIUS" env-default:"10.0"`
	MaxSetpointCelsius              float64               `yaml:"maxSetpointCelsius" env:"MAX_SETPOINT_CELSIUS" env-default:"30.0"`
	Mappings                        []ThermostatMapping   `yaml:"mappings"`
	HardOverrides                   []HardOverride        `yaml:"hardOverrides"`
}

// ThermostatMapping maps a Netatmo room to a Xiaomi sensor
type ThermostatMapping struct {
	RoomName  string `yaml:"roomName"`
	SensorMAC string `yaml:"sensorMAC"`
	RoomID    string `yaml:"roomID"` // Optional: Can be populated at runtime
}

// HardOverride defines a time-based temperature override
type HardOverride struct {
	RoomName string               `yaml:"roomName"`
	Schedule []HardOverrideWindow `yaml:"schedule"`
}

// HardOverrideWindow defines a time window with target temperature
type HardOverrideWindow struct {
	StartTime         string  `yaml:"startTime"` // HH:MM format
	EndTime           string  `yaml:"endTime"`   // HH:MM format
	TargetTemperature float64 `yaml:"targetTemperature"`
}

// AggregatorConfig contains configuration for the BLE sensor aggregator
type AggregatorConfig struct {
	Enabled         bool `yaml:"enabled" env:"AGGREGATOR_ENABLED" env-default:"true"`
	IntervalSeconds int  `yaml:"intervalSeconds" env:"AGGREGATOR_INTERVAL_SECONDS" env-default:"30"`
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
				"cpu":            true,
				"alloc_objects":  true,
				"alloc_space":    true,
				"inuse_objects":  true,
				"inuse_space":    true,
				"goroutines":     true,
				"mutex":          true,
				"block":          true,
			}
			for _, pt := range c.Pyroscope.ProfileTypes {
				if !validTypes[pt] {
					return fmt.Errorf("invalid pyroscope profile type: %s (must be one of: cpu, alloc_objects, alloc_space, inuse_objects, inuse_space, goroutines, mutex, block)", pt)
				}
			}
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

	// Validate thermostat control configuration if enabled
	if c.ThermostatControl.Enabled {
		// Validate temperature threshold
		if c.ThermostatControl.TemperatureThreshold < 0.1 || c.ThermostatControl.TemperatureThreshold > 5.0 {
			return fmt.Errorf("thermostat control temperature threshold must be between 0.1 and 5.0°C, got: %.2f", c.ThermostatControl.TemperatureThreshold)
		}

		// Validate control interval
		if c.ThermostatControl.ControlIntervalSeconds < 1 {
			return fmt.Errorf("thermostat control interval must be at least 1 second")
		}

		// Validate override duration
		if c.ThermostatControl.OverrideDurationMinutes < 1 {
			return fmt.Errorf("thermostat override duration must be at least 1 minute")
		}

		// Validate recheck delay
		if c.ThermostatControl.RecheckDelayMinutes < 1 {
			return fmt.Errorf("thermostat recheck delay must be at least 1 minute")
		}

		// Validate external modification reset minutes
		if c.ThermostatControl.ExternalModificationResetMinutes < 1 {
			return fmt.Errorf("thermostat external modification reset minutes must be at least 1 minute")
		}

		// Validate mappings
		if len(c.ThermostatControl.Mappings) == 0 {
			return fmt.Errorf("at least one thermostat mapping must be configured when thermostat control is enabled")
		}

		// Track room names and validate sensor MACs
		seenRoomNames := make(map[string]bool)
		for i, mapping := range c.ThermostatControl.Mappings {
			// Validate room name
			if mapping.RoomName == "" {
				return fmt.Errorf("thermostat mapping %d: room name is required", i)
			}

			// Note: We allow duplicate room names (one sensor can control multiple rooms)
			seenRoomNames[mapping.RoomName] = true

			// Validate sensor MAC
			if !macAddressRegex.MatchString(mapping.SensorMAC) {
				return fmt.Errorf("thermostat mapping %d (room: %s): invalid sensor MAC address format: %s", i, mapping.RoomName, mapping.SensorMAC)
			}

			// Verify sensor MAC exists in BLE sensor configuration
			macUpper := strings.ToUpper(mapping.SensorMAC)
			found := false
			for _, sensor := range c.BLE.Sensors {
				if strings.ToUpper(sensor.MACAddress) == macUpper {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("thermostat mapping %d (room: %s): sensor MAC %s not found in BLE sensor configuration", i, mapping.RoomName, mapping.SensorMAC)
			}
		}

		// Validate hard overrides
		for i, override := range c.ThermostatControl.HardOverrides {
			// Validate room name
			if override.RoomName == "" {
				return fmt.Errorf("hard override %d: room name is required", i)
			}

			// Validate schedule
			if len(override.Schedule) == 0 {
				return fmt.Errorf("hard override %d (room: %s): at least one schedule window is required", i, override.RoomName)
			}

			// Validate each schedule window
			for j, window := range override.Schedule {
				// Validate time format
				if !timeFormatRegex.MatchString(window.StartTime) {
					return fmt.Errorf("hard override %d (room: %s), window %d: invalid start time format: %s (expected HH:MM)", i, override.RoomName, j, window.StartTime)
				}
				if !timeFormatRegex.MatchString(window.EndTime) {
					return fmt.Errorf("hard override %d (room: %s), window %d: invalid end time format: %s (expected HH:MM)", i, override.RoomName, j, window.EndTime)
				}

				// Validate target temperature
				if window.TargetTemperature < 10.0 || window.TargetTemperature > 30.0 {
					return fmt.Errorf("hard override %d (room: %s), window %d: target temperature must be between 10.0 and 30.0°C, got: %.1f", i, override.RoomName, j, window.TargetTemperature)
				}

				// Validate that start time is before end time (simple string comparison works for HH:MM)
				if window.StartTime >= window.EndTime {
					return fmt.Errorf("hard override %d (room: %s), window %d: start time (%s) must be before end time (%s)", i, override.RoomName, j, window.StartTime, window.EndTime)
				}
			}
		}

		// Verify that Netatmo integration is enabled if thermostat control is enabled
		if !c.Netatmo.Enabled {
			return fmt.Errorf("thermostat control requires Netatmo integration to be enabled")
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

	// Build thermostat mapping info for logging
	mappingInfo := make([]string, len(c.ThermostatControl.Mappings))
	for i, mapping := range c.ThermostatControl.Mappings {
		mappingInfo[i] = fmt.Sprintf("%s → %s", mapping.RoomName, mapping.SensorMAC)
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
		zap.Bool("pyroscope_enabled", c.Pyroscope.Enabled),
		zap.String("pyroscope_server_url", c.Pyroscope.ServerURL),
		zap.String("pyroscope_application_name", c.Pyroscope.ApplicationName),
		zap.Bool("pyroscope_auth_configured", c.Pyroscope.BasicAuthUser != "" && c.Pyroscope.BasicAuthPassword != ""),
		zap.Strings("pyroscope_profile_types", c.Pyroscope.ProfileTypes),
		zap.Int("push_interval_seconds", c.Prometheus.PushIntervalSeconds),
		zap.String("prometheus_url", c.Prometheus.URL),
		zap.String("prometheus_username", c.Prometheus.Username),
		zap.Bool("prometheus_password_set", c.Prometheus.Password != ""),
		zap.Bool("start_at_even_second", c.Prometheus.StartAtEvenSecond),
		zap.Int("buffer_size", c.Prometheus.BufferSize),
		zap.Int("batch_size", c.Prometheus.BatchSize),
		zap.String("log_format", c.Logging.Format),
		zap.String("log_level", c.Logging.Level),
		zap.Bool("thermostat_control_enabled", c.ThermostatControl.Enabled),
		zap.Float64("thermostat_temperature_threshold", c.ThermostatControl.TemperatureThreshold),
		zap.Int("thermostat_control_interval_seconds", c.ThermostatControl.ControlIntervalSeconds),
		zap.Int("thermostat_override_duration_minutes", c.ThermostatControl.OverrideDurationMinutes),
		zap.Int("thermostat_recheck_delay_minutes", c.ThermostatControl.RecheckDelayMinutes),
		zap.Int("thermostat_external_mod_reset_minutes", c.ThermostatControl.ExternalModificationResetMinutes),
		zap.Int("thermostat_mapping_count", len(c.ThermostatControl.Mappings)),
		zap.Strings("thermostat_mappings", mappingInfo),
		zap.Int("thermostat_hard_override_count", len(c.ThermostatControl.HardOverrides)),
	)
}
