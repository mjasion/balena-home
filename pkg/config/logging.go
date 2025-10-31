package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/jsternberg/zap-logfmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Format string `yaml:"logFormat" env:"LOG_FORMAT" env-default:"console"`
	Level  string `yaml:"logLevel" env:"LOG_LEVEL" env-default:"info"`
}

// ValidateLogging validates logging configuration
func ValidateLogging(cfg *LoggingConfig) error {
	// Validate log format
	cfg.Format = strings.ToLower(cfg.Format)
	if cfg.Format != "json" && cfg.Format != "console" && cfg.Format != "logfmt" {
		return fmt.Errorf("logFormat must be 'json', 'console', or 'logfmt', got '%s'", cfg.Format)
	}

	// Validate log level
	cfg.Level = strings.ToLower(cfg.Level)
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLevels[cfg.Level] {
		return fmt.Errorf("logLevel must be one of: debug, info, warn, error, got '%s'", cfg.Level)
	}

	return nil
}

// NewLogger creates a zap logger based on the logging configuration
func NewLogger(cfg *LoggingConfig) (*zap.Logger, error) {
	// Set log level
	var level zapcore.Level
	switch strings.ToLower(cfg.Level) {
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
	if cfg.Format == "logfmt" {
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
	if cfg.Format == "json" {
		zapConfig = zap.NewProductionConfig()
	} else {
		zapConfig = zap.NewDevelopmentConfig()
	}

	zapConfig.Level = zap.NewAtomicLevelAt(level)

	return zapConfig.Build()
}
