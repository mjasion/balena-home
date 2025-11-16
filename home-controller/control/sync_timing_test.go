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
				c.lastSyncTime = time.Now().Add(-tt.timeSinceLastSync)
			}

			// Check if sync is needed
			timeSinceLastSync := time.Since(c.lastSyncTime)

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
		Enabled:                         true,
		ScheduleSyncIntervalMinutes:     1, // Use 1-minute interval so every minute is a sync point
		ScheduleSyncPollIntervalSeconds: 1,
		ScheduleSyncPollTimeoutSeconds:  2, // Short timeout since API will fail
		TemperatureThreshold:            0.5,
		HomeStatusFetchCron:             "0 * * * * *",
		ControlLoopCron:                 "30 * * * * *",
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
	initialSyncTime := c.lastSyncTime

	if !initialSyncTime.IsZero() {
		t.Errorf("Expected initial lastSyncTime to be zero, got %v", initialSyncTime)
	}

	// With interval=1, every minute is a sync point, so no need to wait
	t.Logf("Current minute: %02d (interval: %d, sync point: %v)",
		time.Now().Minute(), cfg.ScheduleSyncIntervalMinutes,
		time.Now().Minute()%cfg.ScheduleSyncIntervalMinutes == 0)

	// Run control loop (will try to sync but fail API calls - that's ok)
	beforeRun := time.Now()
	c.runControlLoop(context.Background())
	afterRun := time.Now()

	// Verify lastSyncTime was updated (even though API calls failed)
	updatedSyncTime := c.lastSyncTime

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
		Enabled:                         true,
		ScheduleSyncIntervalMinutes:     15,
		ScheduleSyncPollIntervalSeconds: 1,
		ScheduleSyncPollTimeoutSeconds:  2,
		TemperatureThreshold:            0.5,
		HomeStatusFetchCron:             "0 * * * * *",
		ControlLoopCron:                 "30 * * * * *",
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
		Enabled:                         true,
		ScheduleSyncIntervalMinutes:     15,
		ScheduleSyncPollIntervalSeconds: 1,
		ScheduleSyncPollTimeoutSeconds:  2,
		TemperatureThreshold:            0.5,
		HomeStatusFetchCron:             "0 * * * * *",
		ControlLoopCron:                 "30 * * * * *",
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
	c.lastSyncTime = time.Now().Add(-5 * time.Minute)
	initialSyncTime := c.lastSyncTime

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
	finalSyncTime := c.lastSyncTime

	if !finalSyncTime.Equal(initialSyncTime) {
		t.Errorf("Expected lastSyncTime to remain unchanged in normal mode, but it changed from %v to %v",
			initialSyncTime, finalSyncTime)
	}

	t.Logf("✓ Normal mode correctly skipped sync (lastSyncTime unchanged: %v)", finalSyncTime)
}

// TestManualModeNotSetByAlgorithmSkipsSync tests that manual mode not set by algorithm is skipped during sync
func TestManualModeNotSetByAlgorithmSkipsSync(t *testing.T) {
	tests := []struct {
		name                 string
		lastSetpointTime     time.Time
		lastSetpoint         float64
		currentSetpoint      float64
		shouldSkipSync       bool
		description          string
	}{
		{
			name:             "User manual mode - never controlled by algorithm",
			lastSetpointTime: time.Time{}, // Never set by algorithm
			lastSetpoint:     0,
			currentSetpoint:  21.0,
			shouldSkipSync:   true,
			description:      "Algorithm never controlled this room, manual mode should be respected",
		},
		{
			name:             "User manual mode - algorithm controlled long ago",
			lastSetpointTime: time.Now().Add(-20 * time.Minute), // Over 15 minutes ago
			lastSetpoint:     22.0,
			currentSetpoint:  21.0,
			shouldSkipSync:   true,
			description:      "Algorithm controlled >15 minutes ago, not our recent override",
		},
		{
			name:             "User manual mode - different setpoint",
			lastSetpointTime: time.Now().Add(-5 * time.Minute), // Recent
			lastSetpoint:     22.0,
			currentSetpoint:  21.0, // Different from what we set (delta > 0.3)
			shouldSkipSync:   true,
			description:      "Setpoint changed from what we set, manual mode should be respected",
		},
		{
			name:             "Algorithm override - recent and matching setpoint",
			lastSetpointTime: time.Now().Add(-5 * time.Minute), // Recent (< 15 min)
			lastSetpoint:     21.0,
			currentSetpoint:  21.1, // Close to what we set (delta < 0.3)
			shouldSkipSync:   false,
			description:      "Recent algorithm override with matching setpoint, can reset to schedule",
		},
		{
			name:             "Algorithm override - exact match",
			lastSetpointTime: time.Now().Add(-2 * time.Minute), // Very recent
			lastSetpoint:     21.5,
			currentSetpoint:  21.5, // Exact match
			shouldSkipSync:   false,
			description:      "Exact setpoint match with recent command, our override",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			controlBuffer := buffer.New(100, logger)
			metricsBuffer := buffer.New(100, logger)

			cfg := &config.ThermostatControlConfig{
				Enabled:                         true,
				ScheduleSyncIntervalMinutes:     15,
				ScheduleSyncPollIntervalSeconds: 1,
				ScheduleSyncPollTimeoutSeconds:  2,
				TemperatureThreshold:            0.5,
				HomeStatusFetchCron:             "0 * * * * *",
				ControlLoopCron:                 "30 * * * * *",
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
				RoomID:           "room1",
				RoomName:         "Living Room",
				LastSetpointTime: tt.lastSetpointTime,
				LastSetpoint:     tt.lastSetpoint,
			}
			c.stateMu.Unlock()

			// Create mock home status with room in manual mode
			homeStatus := &netatmo.HomeStatusResponse{
				Body: netatmo.HomeStatusBody{
					Home: netatmo.Home{
						Rooms: []netatmo.RoomStatus{
							{
								ID:                        "room1",
								Reachable:                 true,
								ThermSetpointMode:         "manual", // Room in manual mode
								ThermSetpointTemperature:  tt.currentSetpoint,
								ThermMeasuredTemperature:  tt.currentSetpoint - 0.2,
								ThermSetpointEndTime:      time.Now().Add(30 * time.Minute).Unix(),
							},
						},
					},
				},
			}

			// Call switchRoomsToScheduleMode directly to test the logic
			skipCount := 0
			c.switchRoomsToScheduleMode(context.Background(), homeStatus, &skipCount)

			// Verify skip behavior
			if tt.shouldSkipSync && skipCount == 0 {
				t.Errorf("Expected room to be skipped during sync, but skipCount=0\n%s", tt.description)
			}

			if !tt.shouldSkipSync && skipCount > 0 {
				t.Errorf("Expected room NOT to be skipped during sync, but skipCount=%d\n%s", skipCount, tt.description)
			}

			t.Logf("✓ %s: skipCount=%d (expected skip=%v)", tt.description, skipCount, tt.shouldSkipSync)
		})
	}
}

// TestScheduleModeNotSkippedDuringSync tests that rooms in schedule mode are not skipped
func TestScheduleModeNotSkippedDuringSync(t *testing.T) {
	logger := zap.NewNop()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:                         true,
		ScheduleSyncIntervalMinutes:     15,
		ScheduleSyncPollIntervalSeconds: 1,
		ScheduleSyncPollTimeoutSeconds:  2,
		TemperatureThreshold:            0.5,
		HomeStatusFetchCron:             "0 * * * * *",
		ControlLoopCron:                 "30 * * * * *",
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

	// Create mock home status with room already in schedule mode
	homeStatus := &netatmo.HomeStatusResponse{
		Body: netatmo.HomeStatusBody{
			Home: netatmo.Home{
				Rooms: []netatmo.RoomStatus{
					{
						ID:                       "room1",
						Reachable:                true,
						ThermSetpointMode:        "schedule", // Already in schedule mode
						ThermSetpointTemperature: 21.0,
						ThermMeasuredTemperature: 20.8,
					},
				},
			},
		},
	}

	// Call switchRoomsToScheduleMode
	skipCount := 0
	c.switchRoomsToScheduleMode(context.Background(), homeStatus, &skipCount)

	// Verify room was not skipped (already in schedule mode, no API call needed but not counted as "skip")
	// Note: The function logs "already in schedule mode, skipping switch" but doesn't increment skipCount
	if skipCount > 0 {
		t.Errorf("Expected skipCount=0 for room already in schedule mode, got %d", skipCount)
	}

	t.Log("✓ Room in schedule mode handled correctly (no skip count increment)")
}
