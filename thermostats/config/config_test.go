package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestLoad_ValidConfig(t *testing.T) {
	// Create temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
ble:
  sensors:
    - name: Sensor1
      id: 1
      macAddress: "A4:C1:38:00:00:01"
    - name: Sensor2
      id: 2
      macAddress: "A4:C1:38:00:00:02"
prometheus:
  pushIntervalSeconds: 15
  prometheusUrl: "https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push"
  prometheusUsername: "123456"
  prometheusPassword: "test-password"
  metricName: "ble_temperature_celsius"
  startAtEvenSecond: true
  bufferSize: 1000
logging:
  logFormat: "console"
  logLevel: "info"
`

	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify BLE config
	if len(cfg.BLE.Sensors) != 2 {
		t.Errorf("Expected 2 sensors, got %d", len(cfg.BLE.Sensors))
	}

	if cfg.BLE.Sensors[0].Name != "Sensor1" {
		t.Errorf("Expected sensor name 'Sensor1', got '%s'", cfg.BLE.Sensors[0].Name)
	}

	// Verify Prometheus config
	if cfg.Prometheus.PushIntervalSeconds != 15 {
		t.Errorf("Expected push interval 15, got %d", cfg.Prometheus.PushIntervalSeconds)
	}

	if cfg.Prometheus.URL != "https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push" {
		t.Errorf("Unexpected Prometheus URL: %s", cfg.Prometheus.URL)
	}

	if cfg.Prometheus.Username != "123456" {
		t.Errorf("Expected username 123456, got %s", cfg.Prometheus.Username)
	}

	if cfg.Prometheus.Password != "test-password" {
		t.Errorf("Expected password test-password, got %s", cfg.Prometheus.Password)
	}

	if cfg.Prometheus.MetricName != "ble_temperature_celsius" {
		t.Errorf("Unexpected metric name: %s", cfg.Prometheus.MetricName)
	}

	if !cfg.Prometheus.StartAtEvenSecond {
		t.Error("Expected startAtEvenSecond to be true")
	}

	if cfg.Prometheus.BufferSize != 1000 {
		t.Errorf("Expected buffer capacity 1000, got %d", cfg.Prometheus.BufferSize)
	}

	// Verify logging config
	if cfg.Logging.Format != "console" {
		t.Errorf("Expected log format console, got %s", cfg.Logging.Format)
	}

	if cfg.Logging.Level != "info" {
		t.Errorf("Expected log level info, got %s", cfg.Logging.Level)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/non/existent/path/config.yaml")
	if err == nil {
		t.Fatal("Expected error for missing file, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	invalidContent := `
this is not: valid: yaml: content
`
	err := os.WriteFile(configPath, []byte(invalidContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	_, err = Load(configPath)
	if err == nil {
		t.Fatal("Expected error for invalid YAML, got nil")
	}
}

func TestValidate_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		expectedErr string
	}{
		{
			name: "No sensors",
			config: Config{
				BLE: BLEConfig{
					Sensors: []SensorConfig{},
				},
				Prometheus: PrometheusConfig{
					PushIntervalSeconds: 15,
					URL:                 "https://example.com",
					Username:            "test",
					BufferSize:          1000,
				},
				Logging: LoggingConfig{
					Format: "console",
					Level:  "info",
				},
			},
			expectedErr: "at least one sensor must be configured",
		},
		{
			name: "Empty Prometheus URL",
			config: Config{
				BLE: BLEConfig{
					Sensors: []SensorConfig{
						{Name: "Test", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
					},
				},
				Prometheus: PrometheusConfig{
					PushIntervalSeconds: 15,
					URL:                 "",
					Username:            "test",
					BufferSize:          1000,
				},
				Logging: LoggingConfig{
					Format: "console",
					Level:  "info",
				},
			},
			expectedErr: "prometheus URL is required",
		},
		{
			name: "Empty Prometheus username",
			config: Config{
				BLE: BLEConfig{
					Sensors: []SensorConfig{
						{Name: "Test", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
					},
				},
				Prometheus: PrometheusConfig{
					PushIntervalSeconds: 15,
					URL:                 "https://example.com",
					Username:            "",
					BufferSize:          1000,
				},
				Logging: LoggingConfig{
					Format: "console",
					Level:  "info",
				},
			},
			expectedErr: "prometheus username is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if err == nil {
				t.Fatal("Expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("Expected error containing '%s', got '%s'", tt.expectedErr, err.Error())
			}
		})
	}
}

func TestValidate_InvalidMACAddress(t *testing.T) {
	tests := []struct {
		name    string
		mac     string
		wantErr bool
	}{
		{"Valid MAC", "A4:C1:38:00:00:01", false},
		{"Valid MAC lowercase", "a4:c1:38:00:00:01", false},
		{"Invalid - missing colon", "A4C13800000001", true},
		{"Invalid - wrong separator", "A4-C1-38-00-00-01", true},
		{"Invalid - too short", "A4:C1:38:00:00", true},
		{"Invalid - too long", "A4:C1:38:00:00:01:02", true},
		{"Invalid - non-hex", "ZZ:C1:38:00:00:01", true},
		{"Empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				BLE: BLEConfig{
					Sensors: []SensorConfig{
						{Name: "Test", ID: 1, MACAddress: tt.mac},
					},
				},
				Prometheus: PrometheusConfig{
					PushIntervalSeconds: 15,
					URL:                 "https://example.com",
					Username:            "test",
					BufferSize:          1000,
				},
				Logging: LoggingConfig{
					Format: "console",
					Level:  "info",
				},
			}

			err := config.Validate()
			if tt.wantErr && err == nil {
				t.Error("Expected validation error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestValidate_PushInterval(t *testing.T) {
	tests := []struct {
		name              string
		pushInterval      int
		wantErr           bool
		expectedErrSubstr string
	}{
		{"Valid interval", 15, false, ""},
		{"Push interval too low", 0, true, "push interval must be at least 1 second"},
		{"Push interval negative", -1, true, "push interval must be at least 1 second"},
		{"Minimum valid interval", 1, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				BLE: BLEConfig{
					Sensors: []SensorConfig{
						{Name: "Test", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
					},
				},
				Prometheus: PrometheusConfig{
					PushIntervalSeconds: tt.pushInterval,
					URL:                 "https://example.com",
					Username:            "test",
					BufferSize:          1000,
				},
				Logging: LoggingConfig{
					Format: "console",
					Level:  "info",
				},
			}

			err := config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Expected validation error, got nil")
				}
				if !strings.Contains(err.Error(), tt.expectedErrSubstr) {
					t.Errorf("Expected error containing '%s', got '%s'", tt.expectedErrSubstr, err.Error())
				}
			} else if err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestValidate_BufferSize(t *testing.T) {
	tests := []struct {
		name           string
		bufferCapacity int
		wantErr        bool
	}{
		{"Valid buffer capacity", 1000, false},
		{"Minimum buffer capacity", 1, false},
		{"Zero buffer capacity", 0, true},
		{"Negative buffer capacity", -1, true},
		{"Large buffer capacity", 1000000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				BLE: BLEConfig{
					Sensors: []SensorConfig{
						{Name: "Test", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
					},
				},
				Prometheus: PrometheusConfig{
					PushIntervalSeconds: 15,
					URL:                 "https://example.com",
					Username:            "test",
					BufferSize:          tt.bufferCapacity,
				},
				Logging: LoggingConfig{
					Format: "console",
					Level:  "info",
				},
			}

			err := config.Validate()
			if tt.wantErr && err == nil {
				t.Error("Expected validation error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestValidate_LogFormat(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr bool
	}{
		{"Valid - console", "console", false},
		{"Valid - json", "json", false},
		{"Valid - uppercase console", "CONSOLE", false},
		{"Valid - uppercase json", "JSON", false},
		{"Invalid format", "xml", true},
		{"Empty format", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				BLE: BLEConfig{
					Sensors: []SensorConfig{
						{Name: "Test", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
					},
				},
				Prometheus: PrometheusConfig{
					PushIntervalSeconds: 15,
					URL:                 "https://example.com",
					Username:            "test",
					BufferSize:          1000,
				},
				Logging: LoggingConfig{
					Format: tt.format,
					Level:  "info",
				},
			}

			err := config.Validate()
			if tt.wantErr && err == nil {
				t.Error("Expected validation error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestValidate_LogLevel(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		wantErr bool
	}{
		{"Valid - debug", "debug", false},
		{"Valid - info", "info", false},
		{"Valid - warn", "warn", false},
		{"Valid - error", "error", false},
		{"Valid - uppercase", "INFO", false},
		{"Invalid level", "trace", true},
		{"Invalid level", "fatal", true},
		{"Empty level", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{
				BLE: BLEConfig{
					Sensors: []SensorConfig{
						{Name: "Test", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
					},
				},
				Prometheus: PrometheusConfig{
					PushIntervalSeconds: 15,
					URL:                 "https://example.com",
					Username:            "test",
					BufferSize:          1000,
				},
				Logging: LoggingConfig{
					Format: "console",
					Level:  tt.level,
				},
			}

			err := config.Validate()
			if tt.wantErr && err == nil {
				t.Error("Expected validation error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}
		})
	}
}

func TestInitLogger_ConsoleFormat(t *testing.T) {
	config := Config{
		Logging: LoggingConfig{
			Format: "console",
			Level:  "info",
		},
	}

	logger, err := config.InitLogger()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if logger == nil {
		t.Fatal("Expected logger, got nil")
	}

	// Cleanup
	logger.Sync()
}

func TestInitLogger_JSONFormat(t *testing.T) {
	config := Config{
		Logging: LoggingConfig{
			Format: "json",
			Level:  "debug",
		},
	}

	logger, err := config.InitLogger()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if logger == nil {
		t.Fatal("Expected logger, got nil")
	}

	// Cleanup
	logger.Sync()
}

func TestInitLogger_AllLevels(t *testing.T) {
	levels := []string{"debug", "info", "warn", "error"}

	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			config := Config{
				Logging: LoggingConfig{
					Format: "console",
					Level:  level,
				},
			}

			logger, err := config.InitLogger()
			if err != nil {
				t.Fatalf("Expected no error for level %s, got: %v", level, err)
			}

			if logger == nil {
				t.Fatalf("Expected logger for level %s, got nil", level)
			}

			logger.Sync()
		})
	}
}

func TestPrintConfig(t *testing.T) {
	config := Config{
		BLE: BLEConfig{
			Sensors: []SensorConfig{
				{Name: "Sensor1", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
				{Name: "Sensor2", ID: 2, MACAddress: "A4:C1:38:00:00:02"},
			},
		},
		Prometheus: PrometheusConfig{
			PushIntervalSeconds: 15,
			URL:                 "https://example.com",
			Username:            "test-user",
			Password:            "secret",
			MetricName:          "test_metric",
			StartAtEvenSecond:   true,
			BufferSize:          1000,
		},
		Logging: LoggingConfig{
			Format: "console",
			Level:  "info",
		},
	}

	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	// This should not panic
	config.PrintConfig(logger)
}

func TestLoad_EnvironmentOverride(t *testing.T) {
	// Create temporary config file with default values
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
ble:
  sensors:
    - name: Sensor1
      id: 1
      macAddress: "A4:C1:38:00:00:01"
prometheus:
  pushIntervalSeconds: 15
  prometheusUrl: "https://example.com"
  prometheusUsername: "default-user"
  metricName: "default_metric"
  bufferSize: 500
logging:
  logFormat: "console"
  logLevel: "info"
`

	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config file: %v", err)
	}

	// Set environment variables to override
	os.Setenv("PROMETHEUS_USERNAME", "env-user")
	os.Setenv("METRIC_NAME", "env_metric")
	os.Setenv("BUFFER_SIZE", "2000")
	defer func() {
		os.Unsetenv("PROMETHEUS_USERNAME")
		os.Unsetenv("METRIC_NAME")
		os.Unsetenv("BUFFER_SIZE")
	}()

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify environment variables override config file
	if cfg.Prometheus.Username != "env-user" {
		t.Errorf("Expected username 'env-user' from env, got %s", cfg.Prometheus.Username)
	}

	if cfg.Prometheus.MetricName != "env_metric" {
		t.Errorf("Expected metric name 'env_metric' from env, got %s", cfg.Prometheus.MetricName)
	}

	if cfg.Prometheus.BufferSize != 2000 {
		t.Errorf("Expected buffer size 2000 from env, got %d", cfg.Prometheus.BufferSize)
	}
}
