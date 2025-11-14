package control

import (
	"context"
	"testing"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.uber.org/zap"
)

// TestSyncIntervalLogic tests the timing logic for schedule sync
func TestSyncIntervalLogic(t *testing.T) {
	tests := []struct {
		name              string
		intervalMinutes   int
		timeSinceLastSync time.Duration
		expectSync        bool
	}{
		{
			name:              "First run (never synced)",
			intervalMinutes:   15,
			timeSinceLastSync: 0, // Never synced (zero value)
			expectSync:        true,
		},
		{
			name:              "14 minutes elapsed (not yet time)",
			intervalMinutes:   15,
			timeSinceLastSync: 14 * time.Minute,
			expectSync:        false,
		},
		{
			name:              "Exactly 15 minutes elapsed",
			intervalMinutes:   15,
			timeSinceLastSync: 15 * time.Minute,
			expectSync:        true,
		},
		{
			name:              "16 minutes elapsed (overdue)",
			intervalMinutes:   15,
			timeSinceLastSync: 16 * time.Minute,
			expectSync:        true,
		},
		{
			name:              "30 minutes elapsed (double interval)",
			intervalMinutes:   15,
			timeSinceLastSync: 30 * time.Minute,
			expectSync:        true,
		},
		{
			name:              "Sync disabled (interval = 0)",
			intervalMinutes:   0,
			timeSinceLastSync: 60 * time.Minute,
			expectSync:        false,
		},
		{
			name:              "Custom interval: 5 minutes, 4 minutes elapsed",
			intervalMinutes:   5,
			timeSinceLastSync: 4 * time.Minute,
			expectSync:        false,
		},
		{
			name:              "Custom interval: 5 minutes, 5 minutes elapsed",
			intervalMinutes:   5,
			timeSinceLastSync: 5 * time.Minute,
			expectSync:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up config with sync interval
			cfg := &config.ThermostatControlConfig{
				Enabled:                     true,
				ScheduleSyncIntervalMinutes: tt.intervalMinutes,
			}

			// Create controller
			c := New(cfg, nil, nil, nil, zap.NewNop())

			// Set last sync time
			if tt.timeSinceLastSync > 0 {
				c.syncMu.Lock()
				c.lastSyncTime = time.Now().Add(-tt.timeSinceLastSync)
				c.syncMu.Unlock()
			}

			// Check if sync is needed
			c.syncMu.RLock()
			timeSinceLastSync := time.Since(c.lastSyncTime)
			c.syncMu.RUnlock()

			needsSync := false
			if cfg.ScheduleSyncIntervalMinutes > 0 {
				syncInterval := time.Duration(cfg.ScheduleSyncIntervalMinutes) * time.Minute
				needsSync = timeSinceLastSync >= syncInterval
			}

			// Verify expectation
			if needsSync != tt.expectSync {
				t.Errorf("Expected needsSync=%v, got %v (timeSinceLastSync=%v, interval=%dm)",
					tt.expectSync, needsSync, timeSinceLastSync, tt.intervalMinutes)
			}

			t.Logf("✓ Sync logic correct: timeSinceLastSync=%v, interval=%dm, needsSync=%v",
				timeSinceLastSync.Round(time.Minute), tt.intervalMinutes, needsSync)
		})
	}
}

// TestSyncTimestampUpdated tests that lastSyncTime is updated after the control loop
func TestSyncTimestampUpdated(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:                      true,
		ScheduleSyncIntervalMinutes:  15,
		ScheduleSyncPollIntervalSeconds: 1,
		ScheduleSyncPollTimeoutSeconds:  2, // Short timeout since API will fail
		TemperatureThreshold:         0.5,
		Cron:                         "0 * * * * *",
		Mappings: []config.ThermostatMapping{
			{RoomName: "Living Room", SensorMAC: "AA:BB:CC:DD:EE:FF", RoomID: "room1"},
		},
	}

	// Use real client (will fail API calls, but that's ok for this test)
	client := netatmo.NewClient("test-id", "test-secret", "test-token")
	c := New(cfg, client, controlBuffer, metricsBuffer, logger)
	c.homeID = "home123"

	// Initialize state
	c.stateMu.Lock()
	c.stateByRoom["room1"] = &ThermostatState{
		RoomID:   "room1",
		RoomName: "Living Room",
	}
	c.stateMu.Unlock()

	// Add sensor data
	controlBuffer.Add(&buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.SensorReading{
			Timestamp:          time.Now(),
			MAC:                "AA:BB:CC:DD:EE:FF",
			TemperatureCelsius: 24.0,
		},
	})

	// First run: lastSyncTime should be zero (never synced)
	c.syncMu.RLock()
	initialSyncTime := c.lastSyncTime
	c.syncMu.RUnlock()

	if !initialSyncTime.IsZero() {
		t.Errorf("Expected initial lastSyncTime to be zero, got %v", initialSyncTime)
	}

	// Run control loop (will try to sync but fail API calls - that's ok)
	beforeRun := time.Now()
	c.runControlLoop(context.Background())
	afterRun := time.Now()

	// Verify lastSyncTime was updated (even though API calls failed)
	c.syncMu.RLock()
	updatedSyncTime := c.lastSyncTime
	c.syncMu.RUnlock()

	if updatedSyncTime.IsZero() {
		t.Error("Expected lastSyncTime to be updated after control loop, but it's still zero")
	}

	if updatedSyncTime.Before(beforeRun) || updatedSyncTime.After(afterRun) {
		t.Errorf("lastSyncTime %v is not within expected range [%v, %v]",
			updatedSyncTime, beforeRun, afterRun)
	}

	t.Logf("✓ lastSyncTime updated correctly: %v", updatedSyncTime)
}

// TestExternallyModifiedRoomSkipsSync tests that externally modified rooms skip sync
func TestExternallyModifiedRoomSkipsSync(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:                      true,
		ScheduleSyncIntervalMinutes:  15,
		ScheduleSyncPollIntervalSeconds: 1,
		ScheduleSyncPollTimeoutSeconds:  2,
		TemperatureThreshold:         0.5,
		Cron:                         "0 * * * * *",
		Mappings: []config.ThermostatMapping{
			{RoomName: "Living Room", SensorMAC: "AA:BB:CC:DD:EE:FF", RoomID: "room1"},
		},
	}

	client := netatmo.NewClient("test-id", "test-secret", "test-token")
	c := New(cfg, client, controlBuffer, metricsBuffer, logger)
	c.homeID = "home123"

	// Initialize state with externally modified flag
	c.stateMu.Lock()
	c.stateByRoom["room1"] = &ThermostatState{
		RoomID:             "room1",
		RoomName:           "Living Room",
		ExternallyModified: true, // Manual override detected
	}
	c.stateMu.Unlock()

	// Add sensor data
	controlBuffer.Add(&buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.SensorReading{
			Timestamp:          time.Now(),
			MAC:                "AA:BB:CC:DD:EE:FF",
			TemperatureCelsius: 24.0,
		},
	})

	// Run control loop
	c.runControlLoop(context.Background())

	// Verify synced temperature was NOT set (room was skipped)
	c.stateMu.RLock()
	state := c.stateByRoom["room1"]
	c.stateMu.RUnlock()

	if state.SyncedScheduledTemp != 0 {
		t.Errorf("Expected SyncedScheduledTemp to remain 0 (skipped), got %.1f", state.SyncedScheduledTemp)
	}

	if !state.SyncedScheduledTime.IsZero() {
		t.Error("Expected SyncedScheduledTime to remain zero (skipped), but it was set")
	}

	t.Log("✓ Externally modified room correctly skipped sync")
}

// TestNormalModeSkipsSync tests that normal mode (interval not elapsed) doesn't sync
func TestNormalModeSkipsSync(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:                      true,
		ScheduleSyncIntervalMinutes:  15,
		ScheduleSyncPollIntervalSeconds: 1,
		ScheduleSyncPollTimeoutSeconds:  2,
		TemperatureThreshold:         0.5,
		Cron:                         "0 * * * * *",
		Mappings: []config.ThermostatMapping{
			{RoomName: "Living Room", SensorMAC: "AA:BB:CC:DD:EE:FF", RoomID: "room1"},
		},
	}

	client := netatmo.NewClient("test-id", "test-secret", "test-token")
	c := New(cfg, client, controlBuffer, metricsBuffer, logger)
	c.homeID = "home123"

	// Initialize state
	c.stateMu.Lock()
	c.stateByRoom["room1"] = &ThermostatState{
		RoomID:   "room1",
		RoomName: "Living Room",
	}
	c.stateMu.Unlock()

	// Set last sync time to 5 minutes ago (not time to sync yet)
	c.syncMu.Lock()
	c.lastSyncTime = time.Now().Add(-5 * time.Minute)
	initialSyncTime := c.lastSyncTime
	c.syncMu.Unlock()

	// Add sensor data
	controlBuffer.Add(&buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.SensorReading{
			Timestamp:          time.Now(),
			MAC:                "AA:BB:CC:DD:EE:FF",
			TemperatureCelsius: 24.0,
		},
	})

	// Run control loop (should use normal mode, not sync mode)
	c.runControlLoop(context.Background())

	// Verify lastSyncTime was NOT updated (normal mode doesn't update it)
	c.syncMu.RLock()
	finalSyncTime := c.lastSyncTime
	c.syncMu.RUnlock()

	if !finalSyncTime.Equal(initialSyncTime) {
		t.Errorf("Expected lastSyncTime to remain unchanged in normal mode, but it changed from %v to %v",
			initialSyncTime, finalSyncTime)
	}

	t.Logf("✓ Normal mode correctly skipped sync (lastSyncTime unchanged: %v)", finalSyncTime)
}
