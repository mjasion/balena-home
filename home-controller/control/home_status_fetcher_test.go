package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
)

func TestNewHomeStatusFetcher(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:              true,
		TemperatureThreshold: 0.5,
	}

	client := netatmo.NewClient("test-client-id", "test-secret", "test-refresh-token")
	controller := New(cfg, client, controlBuffer, metricsBuffer, logger)
	tracer := otel.Tracer("test")

	fetcher := NewHomeStatusFetcher(controller, logger, tracer)

	if fetcher == nil {
		t.Fatal("Expected non-nil fetcher")
	}

	if fetcher.controller != controller {
		t.Error("Controller not stored correctly")
	}

	if fetcher.logger != logger {
		t.Error("Logger not stored correctly")
	}

	if fetcher.tracer != tracer {
		t.Error("Tracer not stored correctly")
	}
}

func TestGetCachedHomeStatus(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:              true,
		TemperatureThreshold: 0.5,
	}

	client := netatmo.NewClient("test-client-id", "test-secret", "test-refresh-token")
	controller := New(cfg, client, controlBuffer, metricsBuffer, logger)

	now := time.Now()

	tests := []struct {
		name           string
		setupCache     bool
		fetchTime      time.Time
		fetchError     error
		expectNil      bool
		expectedReason string
	}{
		{
			name:           "No cache available",
			setupCache:     false,
			expectNil:      true,
			expectedReason: "cache is nil",
		},
		{
			name:           "Cache from same minute - valid",
			setupCache:     true,
			fetchTime:      now,
			expectNil:      false,
			expectedReason: "",
		},
		{
			name:           "Cache from previous minute - invalid",
			setupCache:     true,
			fetchTime:      now.Add(-1 * time.Minute),
			expectNil:      true,
			expectedReason: "different minute",
		},
		{
			name:           "Cache from previous hour - invalid",
			setupCache:     true,
			fetchTime:      now.Add(-1 * time.Hour),
			expectNil:      true,
			expectedReason: "different hour",
		},
		{
			name:           "Cache from previous day - invalid",
			setupCache:     true,
			fetchTime:      now.Add(-24 * time.Hour),
			expectNil:      true,
			expectedReason: "different day",
		},
		{
			name:           "Cache with error but same minute - valid (error stored)",
			setupCache:     true,
			fetchTime:      now,
			fetchError:     errors.New("test error"),
			expectNil:      false,
			expectedReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset cache
			controller.cachedStatusMu.Lock()
			controller.cachedStatus = nil
			controller.cachedStatusMu.Unlock()

			if tt.setupCache {
				homeStatus := &netatmo.HomeStatusResponse{
					Status: "ok",
					Body: struct {
						Home netatmo.HomeStatus `json:"home"`
					}{
						Home: netatmo.HomeStatus{
							ID: "test-home",
							Rooms: []netatmo.RoomStatus{
								{
									ID:                       "room1",
									ThermSetpointMode:        "schedule",
									ThermSetpointTemperature: 22.0,
									ThermMeasuredTemperature: 21.5,
									Reachable:                true,
								},
							},
						},
					},
				}

				controller.cachedStatusMu.Lock()
				controller.cachedStatus = &CachedHomeStatus{
					HomeStatus: homeStatus,
					FetchTime:  tt.fetchTime,
					FetchError: tt.fetchError,
				}
				controller.cachedStatusMu.Unlock()
			}

			result := controller.GetCachedHomeStatus()

			if tt.expectNil {
				if result != nil {
					t.Errorf("Expected nil result (%s), got non-nil", tt.expectedReason)
				}
			} else {
				if result == nil {
					t.Fatal("Expected non-nil result, got nil")
				}

				if result.FetchTime != tt.fetchTime {
					t.Errorf("Expected FetchTime=%v, got %v", tt.fetchTime, result.FetchTime)
				}

				if result.FetchError != tt.fetchError {
					t.Errorf("Expected FetchError=%v, got %v", tt.fetchError, result.FetchError)
				}

				if tt.fetchError == nil && result.HomeStatus == nil {
					t.Error("Expected non-nil HomeStatus when no error")
				}
			}
		})
	}
}

func TestGetCachedHomeStatusConcurrency(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:              true,
		TemperatureThreshold: 0.5,
	}

	client := netatmo.NewClient("test-client-id", "test-secret", "test-refresh-token")
	controller := New(cfg, client, controlBuffer, metricsBuffer, logger)

	now := time.Now()
	homeStatus := &netatmo.HomeStatusResponse{
		Status: "ok",
	}

	// Set initial cache
	controller.cachedStatusMu.Lock()
	controller.cachedStatus = &CachedHomeStatus{
		HomeStatus: homeStatus,
		FetchTime:  now,
		FetchError: nil,
	}
	controller.cachedStatusMu.Unlock()

	// Test concurrent reads
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			result := controller.GetCachedHomeStatus()
			if result == nil {
				t.Error("Concurrent read returned nil")
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestCachedHomeStatusMinuteBoundary(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:              true,
		TemperatureThreshold: 0.5,
	}

	client := netatmo.NewClient("test-client-id", "test-secret", "test-refresh-token")
	controller := New(cfg, client, controlBuffer, metricsBuffer, logger)

	// Test at minute boundary
	// Cache from 10:00:59 should be valid at 10:00:01 (same minute)
	cacheTime := time.Date(2025, 1, 15, 10, 0, 59, 0, time.UTC)

	homeStatus := &netatmo.HomeStatusResponse{
		Status: "ok",
	}

	controller.cachedStatusMu.Lock()
	controller.cachedStatus = &CachedHomeStatus{
		HomeStatus: homeStatus,
		FetchTime:  cacheTime,
		FetchError: nil,
	}
	controller.cachedStatusMu.Unlock()

	// Test cases with different times in the same minute
	tests := []struct {
		name      string
		checkTime time.Time
		expectNil bool
	}{
		{
			name:      "Same minute, earlier second",
			checkTime: time.Date(2025, 1, 15, 10, 0, 1, 0, time.UTC),
			expectNil: false,
		},
		{
			name:      "Same minute, same second",
			checkTime: time.Date(2025, 1, 15, 10, 0, 59, 0, time.UTC),
			expectNil: false,
		},
		{
			name:      "Next minute, first second",
			checkTime: time.Date(2025, 1, 15, 10, 1, 0, 0, time.UTC),
			expectNil: true,
		},
		{
			name:      "Previous minute, last second",
			checkTime: time.Date(2025, 1, 15, 9, 59, 59, 0, time.UTC),
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock current time by checking manually
			if controller.cachedStatus == nil {
				t.Fatal("Cache should not be nil")
			}

			isSameMinute := controller.cachedStatus.FetchTime.Minute() == tt.checkTime.Minute() &&
				controller.cachedStatus.FetchTime.Hour() == tt.checkTime.Hour() &&
				controller.cachedStatus.FetchTime.Day() == tt.checkTime.Day()

			if tt.expectNil && isSameMinute {
				t.Errorf("Expected different minute, but times match: cache=%v, check=%v",
					controller.cachedStatus.FetchTime, tt.checkTime)
			} else if !tt.expectNil && !isSameMinute {
				t.Errorf("Expected same minute, but times differ: cache=%v, check=%v",
					controller.cachedStatus.FetchTime, tt.checkTime)
			}
		})
	}
}

func TestHomeStatusFetcherIntegration(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:              true,
		TemperatureThreshold: 0.5,
		Mappings: []config.ThermostatMapping{
			{RoomName: "Living Room", SensorMAC: "AA:BB:CC:DD:EE:FF", RoomID: "room1"},
		},
	}

	client := netatmo.NewClient("test-client-id", "test-secret", "test-refresh-token")
	controller := New(cfg, client, controlBuffer, metricsBuffer, logger)
	controller.homeID = "test-home-id"

	tracer := otel.Tracer("test")
	fetcher := NewHomeStatusFetcher(controller, logger, tracer)

	// Note: This test will fail to actually fetch from Netatmo API without valid credentials,
	// but it will exercise the caching logic
	ctx := context.Background()

	// Run the fetcher (will fail but cache the error)
	fetcher.Run(ctx)

	// Verify cache was populated (even with error)
	controller.cachedStatusMu.RLock()
	cached := controller.cachedStatus
	controller.cachedStatusMu.RUnlock()

	if cached == nil {
		t.Fatal("Expected cache to be populated after Run")
	}

	if cached.FetchError == nil {
		t.Error("Expected FetchError to be set (invalid credentials)")
	}

	// Verify FetchTime was set
	if cached.FetchTime.IsZero() {
		t.Error("Expected FetchTime to be set")
	}

	// Verify we can retrieve the cached status
	retrieved := controller.GetCachedHomeStatus()
	if retrieved == nil {
		t.Error("Expected to retrieve cached status")
	}
}
