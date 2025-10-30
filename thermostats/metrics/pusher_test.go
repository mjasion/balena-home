package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"go.uber.org/zap"
)

// Helper function to wrap BLE sensor readings in Reading struct for tests
func wrapBLEReadings(sensorReadings []*buffer.SensorReading) []*buffer.Reading {
	readings := make([]*buffer.Reading, len(sensorReadings))
	for i, sr := range sensorReadings {
		readings[i] = &buffer.Reading{
			Type: buffer.ReadingTypeBLE,
			BLE:  sr,
		}
	}
	return readings
}

// Helper function to create a test pusher
func newTestPusher(url, username, password string, logger *zap.Logger) *Pusher {
	buf := buffer.New(1000, logger)
	return New(url, username, password, buf, 30, 1000, logger)
}

func TestNew(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher(
		"https://example.com/api/push",
		"test-user",
		"test-password",
		logger,
	)

	if pusher == nil {
		t.Fatal("Expected pusher to be created, got nil")
	}

	if pusher.url != "https://example.com/api/push" {
		t.Errorf("Expected URL https://example.com/api/push, got %s", pusher.url)
	}

	if pusher.username != "test-user" {
		t.Errorf("Expected username test-user, got %s", pusher.username)
	}

	if pusher.password != "test-password" {
		t.Errorf("Expected password test-password, got %s", pusher.password)
	}

	if pusher.client == nil {
		t.Error("Expected HTTP client to be initialized")
	}
}

func TestPush_EmptyReadings(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher("https://example.com", "user", "pass", logger)

	err := pusher.Push(context.Background(), wrapBLEReadings([]*buffer.SensorReading{}))
	if err != nil {
		t.Errorf("Expected no error for empty readings, got: %v", err)
	}
}

func TestPush_Success(t *testing.T) {
	// Create test server
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		// Verify request method
		if r.Method != "POST" {
			t.Errorf("Expected POST method, got %s", r.Method)
		}

		// Verify headers
		if r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("Expected Content-Type application/x-protobuf, got %s", r.Header.Get("Content-Type"))
		}

		if r.Header.Get("Content-Encoding") != "snappy" {
			t.Errorf("Expected Content-Encoding snappy, got %s", r.Header.Get("Content-Encoding"))
		}

		// Verify basic auth
		username, password, ok := r.BasicAuth()
		if !ok {
			t.Error("Expected basic auth to be present")
		}
		if username != "test-user" {
			t.Errorf("Expected username test-user, got %s", username)
		}
		if password != "test-password" {
			t.Errorf("Expected password test-password, got %s", password)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher(server.URL, "test-user", "test-password", logger)

	// Create test readings
	readings := []*buffer.SensorReading{
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:01",
			TemperatureCelsius: 22.5,
			HumidityPercent:    65,
			BatteryPercent:     95,
			BatteryVoltageMV:   3000,
			FrameCounter:       1,
			RSSI:               -65,
		},
		{
			Timestamp:          time.Now().Add(time.Second),
			MAC:                "A4:C1:38:00:00:01",
			TemperatureCelsius: 22.6,
			HumidityPercent:    64,
			BatteryPercent:     95,
			BatteryVoltageMV:   3000,
			FrameCounter:       2,
			RSSI:               -66,
		},
	}

	err := pusher.Push(context.Background(), wrapBLEReadings(readings))
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if requestCount != 1 {
		t.Errorf("Expected 1 request, got %d", requestCount)
	}
}

func TestPush_MultipleSensors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher(server.URL, "user", "pass", logger)

	// Create readings from multiple sensors
	readings := []*buffer.SensorReading{
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:01",
			TemperatureCelsius: 22.5,
			HumidityPercent:    65,
			BatteryPercent:     95,
			BatteryVoltageMV:   3000,
			FrameCounter:       1,
			RSSI:               -65,
		},
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:02",
			TemperatureCelsius: 23.1,
			HumidityPercent:    60,
			BatteryPercent:     90,
			BatteryVoltageMV:   2950,
			FrameCounter:       5,
			RSSI:               -70,
		},
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:03",
			TemperatureCelsius: 21.8,
			HumidityPercent:    68,
			BatteryPercent:     98,
			BatteryVoltageMV:   3050,
			FrameCounter:       10,
			RSSI:               -62,
		},
	}

	err := pusher.Push(context.Background(), wrapBLEReadings(readings))
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func TestPush_ServerError(t *testing.T) {
	// Create test server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher(server.URL, "user", "pass", logger)

	readings := []*buffer.SensorReading{
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:01",
			TemperatureCelsius: 22.5,
		},
	}

	err := pusher.Push(context.Background(), wrapBLEReadings(readings))
	if err == nil {
		t.Fatal("Expected error for server error, got nil")
	}
}

func TestPush_WithRetries(t *testing.T) {
	// Server that fails first 2 attempts, succeeds on 3rd
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher(server.URL, "user", "pass", logger)

	readings := []*buffer.SensorReading{
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:01",
			TemperatureCelsius: 22.5,
		},
	}

	err := pusher.Push(context.Background(), wrapBLEReadings(readings))
	if err != nil {
		t.Fatalf("Expected success after retries, got: %v", err)
	}

	if attemptCount != 3 {
		t.Errorf("Expected 3 attempts, got %d", attemptCount)
	}
}

func TestPush_MaxRetriesExceeded(t *testing.T) {
	// Server that always fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher(server.URL, "user", "pass", logger)

	readings := []*buffer.SensorReading{
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:01",
			TemperatureCelsius: 22.5,
		},
	}

	err := pusher.Push(context.Background(), wrapBLEReadings(readings))
	if err == nil {
		t.Fatal("Expected error after max retries, got nil")
	}
}

func TestPush_ContextCancellation(t *testing.T) {
	// Server with delayed response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher(server.URL, "user", "pass", logger)

	readings := []*buffer.SensorReading{
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:01",
			TemperatureCelsius: 22.5,
		},
	}

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := pusher.Push(ctx, wrapBLEReadings(readings))
	if err == nil {
		t.Fatal("Expected error for context cancellation, got nil")
	}
}

func TestBuildWriteRequest(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher("https://example.com", "user", "pass", logger)

	now := time.Now()
	readings := []*buffer.SensorReading{
		{
			Timestamp:          now,
			MAC:                "A4:C1:38:00:00:01",
			SensorName:         "Living Room",
			SensorID:           1,
			TemperatureCelsius: 22.5,
			HumidityPercent:    50,
			BatteryPercent:     90,
		},
		{
			Timestamp:          now.Add(time.Second),
			MAC:                "A4:C1:38:00:00:01",
			SensorName:         "Living Room",
			SensorID:           1,
			TemperatureCelsius: 22.6,
			HumidityPercent:    51,
			BatteryPercent:     90,
		},
		{
			Timestamp:          now,
			MAC:                "A4:C1:38:00:00:02",
			SensorName:         "Bedroom",
			SensorID:           2,
			TemperatureCelsius: 23.1,
			HumidityPercent:    55,
			BatteryPercent:     85,
		},
	}

	writeReq, err := pusher.buildWriteRequest(context.Background(), wrapBLEReadings(readings))
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if writeReq == nil {
		t.Fatal("Expected write request, got nil")
	}

	// Should have 6 time series (3 metrics per sensor: temp, humidity, battery × 2 sensors)
	if len(writeReq.Timeseries) != 6 {
		t.Errorf("Expected 6 time series, got %d", len(writeReq.Timeseries))
	}

	// Count time series by metric type
	metricCounts := make(map[string]int)
	for _, ts := range writeReq.Timeseries {
		for _, label := range ts.Labels {
			if label.Name == "__name__" {
				metricCounts[label.Value]++
			}
		}
	}

	// Verify we have 2 of each metric type (one per sensor)
	expectedMetrics := map[string]int{
		"ble_temperature_celsius": 2,
		"ble_humidity_percent":    2,
		"ble_battery_percent":     2,
	}

	for metricName, expectedCount := range expectedMetrics {
		if count, exists := metricCounts[metricName]; !exists || count != expectedCount {
			t.Errorf("Expected %d time series for metric %s, got %d", expectedCount, metricName, count)
		}
	}

	// Verify each time series has the required labels
	for _, ts := range writeReq.Timeseries {
		foundName := false
		foundSensorID := false
		foundMAC := false
		foundSensorName := false

		for _, label := range ts.Labels {
			if label.Name == "__name__" {
				foundName = true
			}
			if label.Name == "sensor_id" {
				foundSensorID = true
			}
			if label.Name == "mac" {
				foundMAC = true
			}
			if label.Name == "sensor_name" {
				foundSensorName = true
			}
		}

		if !foundName {
			t.Error("Expected __name__ label in time series")
		}
		if !foundSensorID {
			t.Error("Expected sensor_id label in time series")
		}
		if !foundMAC {
			t.Error("Expected mac label in time series")
		}
		if !foundSensorName {
			t.Error("Expected sensor_name label in time series")
		}
	}
}

func TestLastPushTime(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	pusher := newTestPusher("https://example.com", "user", "pass", logger)

	// Initial last push time should be recent
	lastPush := pusher.LastPushTime()
	if time.Since(lastPush) > time.Second {
		t.Error("Expected lastPush to be initialized to recent time")
	}

	// After successful push, last push time should update
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pusher.url = server.URL

	readings := []*buffer.SensorReading{
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:01",
			TemperatureCelsius: 22.5,
		},
	}

	// Wait a bit to ensure time difference
	time.Sleep(10 * time.Millisecond)

	err := pusher.Push(context.Background(), wrapBLEReadings(readings))
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	newLastPush := pusher.LastPushTime()
	if !newLastPush.After(lastPush) {
		t.Error("Expected lastPush to be updated after successful push")
	}
}

func TestPush_NoBasicAuth(t *testing.T) {
	authProvided := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, ok := r.BasicAuth()
		authProvided = ok
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	// Create pusher with empty credentials
	pusher := newTestPusher(server.URL, "", "", logger)

	readings := []*buffer.SensorReading{
		{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:00:00:01",
			TemperatureCelsius: 22.5,
		},
	}

	err := pusher.Push(context.Background(), wrapBLEReadings(readings))
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if authProvided {
		t.Error("Expected no basic auth when credentials are empty")
	}
}

func TestRoundToTenSeconds(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{
			name:     "Round down - 3 seconds",
			input:    time.Date(2024, 1, 1, 12, 0, 3, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name:     "Round down - 4.9 seconds",
			input:    time.Date(2024, 1, 1, 12, 0, 4, 900000000, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name:     "Round up - 5 seconds",
			input:    time.Date(2024, 1, 1, 12, 0, 5, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 10, 0, time.UTC),
		},
		{
			name:     "Round up - 7 seconds",
			input:    time.Date(2024, 1, 1, 12, 0, 7, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 10, 0, time.UTC),
		},
		{
			name:     "Round down - 13 seconds to :10",
			input:    time.Date(2024, 1, 1, 12, 0, 13, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 10, 0, time.UTC),
		},
		{
			name:     "Round up - 15 seconds to :20",
			input:    time.Date(2024, 1, 1, 12, 0, 15, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 20, 0, time.UTC),
		},
		{
			name:     "Already rounded to :00",
			input:    time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name:     "Already rounded to :10",
			input:    time.Date(2024, 1, 1, 12, 0, 10, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 10, 0, time.UTC),
		},
		{
			name:     "Already rounded to :50",
			input:    time.Date(2024, 1, 1, 12, 0, 50, 0, time.UTC),
			expected: time.Date(2024, 1, 1, 12, 0, 50, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := roundToTenSeconds(tt.input)
			if !result.Equal(tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
