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

// TestScheduleSync_HybridBehavior_Unchanged tests that when schedule is unchanged,
// the previous override is restored immediately without re-evaluation
func TestScheduleSync_HybridBehavior_Unchanged(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                      true,
		DryRun:                       true,
		TemperatureThreshold:         0.2,
		OverrideDurationMinutes:      15,
		ExtensionThresholdMinutes:    4,
		ScheduleSyncIntervalMinutes:  15,
		MinSetpointCelsius:           10.0,
		MaxSetpointCelsius:           30.0,
		Mappings: []config.ThermostatMapping{
			{
				RoomName:  "Bathroom",
				RoomID:    "test-room-1",
				SensorMAC: "A4:C1:38:F1:2D:0D",
			},
		},
	}

	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)
	mockClient := &netatmo.Client{}

	controller := New(cfg, mockClient, controlBuffer, metricsBuffer, logger)
	controller.homeID = "test-home"

	// Initialize room state with existing override and synced schedule
	now := time.Now()
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:              "test-room-1",
		RoomName:            "Bathroom",
		LastSetpoint:        24.0,
		LastSetpointTime:    now.Add(-10 * time.Minute),
		OverrideEndTime:     now.Add(5 * time.Minute),
		SyncedScheduledTemp: 24.5, // Previous synced schedule
		SyncedScheduledTime: now.Add(-15 * time.Minute),
	}
	controller.stateMu.Unlock()

	ctx := context.Background()

	// Create previousOverrides map simulating active override before sync
	previousOverrides := map[string]*PreviousOverride{
		"test-room-1": {
			RoomID:    "test-room-1",
			Setpoint:  24.0,
			EndTime:   now.Add(5 * time.Minute),
			WasActive: true,
			// ScheduleChanged will be set by pollUntilAllRoomsSynced
		},
	}

	// Simulate polling - schedule UNCHANGED (still 24.5°C)
	controller.stateMu.Lock()
	state := controller.stateByRoom["test-room-1"]
	previousScheduledTemp := state.SyncedScheduledTemp // 24.5
	newScheduledTemp := 24.5                           // UNCHANGED

	// Check if schedule changed
	scheduleChanged := false
	if previousScheduledTemp > 0 && (newScheduledTemp-previousScheduledTemp) > 0.1 {
		scheduleChanged = true
	}

	// Mark in state and override map
	state.ScheduleJustChanged = scheduleChanged
	if override, exists := previousOverrides["test-room-1"]; exists {
		override.ScheduleChanged = scheduleChanged
	}

	state.SyncedScheduledTemp = newScheduledTemp
	state.SyncedScheduledTime = now
	controller.stateMu.Unlock()

	// Verify schedule did NOT change
	if previousOverrides["test-room-1"].ScheduleChanged {
		t.Error("Expected ScheduleChanged=false when schedule unchanged")
	}

	// Test restorePreviousOverrides - should restore because schedule unchanged
	controller.restorePreviousOverrides(ctx, previousOverrides)

	// Verify ScheduleJustChanged flag is NOT set (no bypass needed)
	controller.stateMu.RLock()
	if state.ScheduleJustChanged {
		t.Error("Expected ScheduleJustChanged=false when schedule unchanged")
	}
	controller.stateMu.RUnlock()

	t.Log("✅ Schedule unchanged: Previous override should be restored, no re-evaluation needed")
}

// TestScheduleSync_HybridBehavior_Changed tests that when schedule changes,
// the previous override is NOT restored and delayed execution is bypassed
func TestScheduleSync_HybridBehavior_Changed(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                      true,
		DryRun:                       true,
		TemperatureThreshold:         0.2,
		OverrideDurationMinutes:      15,
		ExtensionThresholdMinutes:    4,
		ScheduleSyncIntervalMinutes:  15,
		MinSetpointCelsius:           10.0,
		MaxSetpointCelsius:           30.0,
		Mappings: []config.ThermostatMapping{
			{
				RoomName:  "Bathroom",
				RoomID:    "test-room-1",
				SensorMAC: "A4:C1:38:F1:2D:0D",
			},
		},
	}

	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)
	mockClient := &netatmo.Client{}

	controller := New(cfg, mockClient, controlBuffer, metricsBuffer, logger)
	controller.homeID = "test-home"

	// Initialize room state with existing override and synced schedule
	now := time.Now()
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:              "test-room-1",
		RoomName:            "Bathroom",
		LastSetpoint:        24.0,
		LastSetpointTime:    now.Add(-10 * time.Minute),
		OverrideEndTime:     now.Add(5 * time.Minute),
		SyncedScheduledTemp: 24.5, // Previous synced schedule
		SyncedScheduledTime: now.Add(-15 * time.Minute),
	}
	controller.stateMu.Unlock()

	ctx := context.Background()

	// Create previousOverrides map simulating active override before sync
	previousOverrides := map[string]*PreviousOverride{
		"test-room-1": {
			RoomID:    "test-room-1",
			Setpoint:  24.0,
			EndTime:   now.Add(5 * time.Minute),
			WasActive: true,
			// ScheduleChanged will be set by pollUntilAllRoomsSynced
		},
	}

	// Simulate polling - schedule CHANGED (24.5°C → 22.0°C)
	controller.stateMu.Lock()
	state := controller.stateByRoom["test-room-1"]
	previousScheduledTemp := state.SyncedScheduledTemp // 24.5
	newScheduledTemp := 22.0                           // CHANGED!

	// Check if schedule changed
	scheduleChanged := false
	if previousScheduledTemp > 0 && (newScheduledTemp-previousScheduledTemp) > 0.1 || (previousScheduledTemp-newScheduledTemp) > 0.1 {
		scheduleChanged = true
	}

	// Mark in state and override map
	state.ScheduleJustChanged = scheduleChanged
	if override, exists := previousOverrides["test-room-1"]; exists {
		override.ScheduleChanged = scheduleChanged
	}

	state.SyncedScheduledTemp = newScheduledTemp
	state.SyncedScheduledTime = now
	controller.stateMu.Unlock()

	// Verify schedule DID change
	if !previousOverrides["test-room-1"].ScheduleChanged {
		t.Error("Expected ScheduleChanged=true when schedule changed")
	}

	// Test restorePreviousOverrides - should SKIP restoration because schedule changed
	controller.restorePreviousOverrides(ctx, previousOverrides)

	// Verify ScheduleJustChanged flag IS set (bypass will be triggered)
	controller.stateMu.RLock()
	if !state.ScheduleJustChanged {
		t.Error("Expected ScheduleJustChanged=true when schedule changed")
	}
	controller.stateMu.RUnlock()

	// Test delayed execution bypass
	stateCopy := state.Copy()
	shouldDelay, reason := controller.checkDelayedExecution(cfg.Mappings[0], &stateCopy, 22.5)

	if shouldDelay {
		t.Errorf("Expected NO delay (bypass) when schedule changed, got: %s", reason)
	}

	// Verify flag was cleared after bypass
	controller.stateMu.RLock()
	if state.ScheduleJustChanged {
		t.Error("Expected ScheduleJustChanged to be cleared after bypass")
	}
	controller.stateMu.RUnlock()

	t.Log("✅ Schedule changed: Override NOT restored, delayed execution bypassed, immediate re-evaluation")
}

// TestScheduleSync_HybridBehavior_FirstSync tests behavior when no previous sync exists
func TestScheduleSync_HybridBehavior_FirstSync(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                      true,
		DryRun:                       true,
		TemperatureThreshold:         0.2,
		OverrideDurationMinutes:      15,
		ExtensionThresholdMinutes:    4,
		ScheduleSyncIntervalMinutes:  15,
		MinSetpointCelsius:           10.0,
		MaxSetpointCelsius:           30.0,
		Mappings: []config.ThermostatMapping{
			{
				RoomName:  "Bathroom",
				RoomID:    "test-room-1",
				SensorMAC: "A4:C1:38:F1:2D:0D",
			},
		},
	}

	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)
	mockClient := &netatmo.Client{}

	controller := New(cfg, mockClient, controlBuffer, metricsBuffer, logger)
	controller.homeID = "test-home"

	// Initialize room state WITHOUT previous sync (first sync)
	now := time.Now()
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:              "test-room-1",
		RoomName:            "Bathroom",
		LastSetpoint:        24.0,
		LastSetpointTime:    now.Add(-10 * time.Minute),
		OverrideEndTime:     now.Add(5 * time.Minute),
		SyncedScheduledTemp: 0, // NO previous sync
		SyncedScheduledTime: time.Time{},
	}
	controller.stateMu.Unlock()

	ctx := context.Background()

	// Create previousOverrides map
	previousOverrides := map[string]*PreviousOverride{
		"test-room-1": {
			RoomID:    "test-room-1",
			Setpoint:  24.0,
			EndTime:   now.Add(5 * time.Minute),
			WasActive: true,
		},
	}

	// Simulate first sync
	controller.stateMu.Lock()
	state := controller.stateByRoom["test-room-1"]
	previousScheduledTemp := state.SyncedScheduledTemp // 0 (no previous)
	newScheduledTemp := 24.5

	// Check if schedule changed (should be false on first sync)
	scheduleChanged := false
	if previousScheduledTemp > 0 && (newScheduledTemp-previousScheduledTemp) > 0.1 {
		scheduleChanged = true
	}

	// Mark in state and override map
	state.ScheduleJustChanged = scheduleChanged
	if override, exists := previousOverrides["test-room-1"]; exists {
		override.ScheduleChanged = scheduleChanged
	}

	state.SyncedScheduledTemp = newScheduledTemp
	state.SyncedScheduledTime = now
	controller.stateMu.Unlock()

	// Verify schedule did NOT change (first sync)
	if previousOverrides["test-room-1"].ScheduleChanged {
		t.Error("Expected ScheduleChanged=false on first sync (no previous to compare)")
	}

	// Test restorePreviousOverrides - should restore on first sync
	controller.restorePreviousOverrides(ctx, previousOverrides)

	// Verify ScheduleJustChanged flag is NOT set
	controller.stateMu.RLock()
	if state.ScheduleJustChanged {
		t.Error("Expected ScheduleJustChanged=false on first sync")
	}
	controller.stateMu.RUnlock()

	t.Log("✅ First sync: Previous override restored, treated as 'unchanged'")
}
