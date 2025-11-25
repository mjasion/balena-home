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

// TestDelayedExecution_FeedbackLoopPrevention verifies that delayed execution prevents feedback loops
//
// Simulates the actual runaway bug scenario from 2025-11-18:
// - Without delayed execution: setpoint increases every iteration (feedback loop)
// - With delayed execution: target keeps changing, so never executes
func TestDelayedExecution_FeedbackLoopPrevention(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                   true,
		DryRun:                    true,
		TemperatureThreshold:      0.2,
		OverrideDurationMinutes:   10,
		ExtensionThresholdMinutes: 2,
		MinSetpointCelsius:        10.0,
		MaxSetpointCelsius:        30.0,
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

	// Initialize room state
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:   "test-room-1",
		RoomName: "Bathroom",
	}
	controller.stateMu.Unlock()

	// Add sensor reading (25.3°C - actual value from incident)
	reading := &buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.SensorReading{
			Timestamp:          time.Now(),
			MAC:                "A4:C1:38:F1:2D:0D",
			TemperatureCelsius: 25.3,
		},
	}
	controlBuffer.Add(reading)

	ctx := context.Background()

	// Iteration 1: scheduled=24.0, should calculate 24.5°C → mark pending, don't execute
	roomStatusMap1 := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                       "test-room-1",
			Reachable:                true,
			ThermSetpointMode:        "schedule",
			ThermSetpointTemperature: 24.0,
			ThermMeasuredTemperature: 26.0,
			HeatingPowerRequest:      0,
		},
	}

	decision1 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap1)
	t.Logf("Iteration 1: action=%s, reason=%s", decision1.Action, decision1.Reason)

	if decision1.Action != "skip" {
		t.Fatalf("Iteration 1: Expected skip (marking pending), got %s", decision1.Action)
	}

	// Verify pending setpoint was set
	controller.stateMu.RLock()
	state := controller.stateByRoom["test-room-1"]
	if state.PendingSetpoint == 0 {
		t.Error("Expected PendingSetpoint to be set after iteration 1")
	}
	expectedPending1 := 24.5 // scheduled 24.0 + offset 0.7 = 24.7 → rounds to 24.5
	if state.PendingSetpoint != expectedPending1 {
		t.Errorf("Expected PendingSetpoint=%.1f, got %.1f", expectedPending1, state.PendingSetpoint)
	}
	controller.stateMu.RUnlock()

	t.Logf("✅ Iteration 1: Marked pending=%.1f°C (not executed)", state.PendingSetpoint)

	// Iteration 2: FEEDBACK LOOP - scheduled becomes 24.5 (if we had executed)
	// But since we didn't execute, scheduled is still 24.0
	// However, to simulate the feedback bug, let's pretend setpoint changed to 24.5
	roomStatusMap2 := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                       "test-room-1",
			Reachable:                true,
			ThermSetpointMode:        "manual",
			ThermSetpointTemperature: 24.5, // Would calculate 25.0°C (different from pending)
			ThermMeasuredTemperature: 26.0,
			HeatingPowerRequest:      0,
		},
	}

	decision2 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap2)
	t.Logf("Iteration 2: action=%s, reason=%s", decision2.Action, decision2.Reason)

	if decision2.Action != "skip" {
		t.Fatalf("Iteration 2: Expected skip (target changed), got %s", decision2.Action)
	}

	// Verify pending was updated to new target
	controller.stateMu.RLock()
	expectedPending2 := 25.0 // scheduled 24.5 + offset 0.7 = 25.2 → rounds to 25.0
	if state.PendingSetpoint != expectedPending2 {
		t.Errorf("Expected PendingSetpoint updated to %.1f, got %.1f", expectedPending2, state.PendingSetpoint)
	}
	controller.stateMu.RUnlock()

	t.Logf("✅ Iteration 2: Target changed, updated pending=%.1f°C (not executed)", state.PendingSetpoint)

	// Iteration 3: FEEDBACK LOOP continues - scheduled becomes 25.0
	roomStatusMap3 := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                       "test-room-1",
			Reachable:                true,
			ThermSetpointMode:        "manual",
			ThermSetpointTemperature: 25.0, // Would calculate 25.5°C (different from pending)
			ThermMeasuredTemperature: 26.0,
			HeatingPowerRequest:      0,
		},
	}

	decision3 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap3)
	t.Logf("Iteration 3: action=%s, reason=%s", decision3.Action, decision3.Reason)

	if decision3.Action != "skip" {
		t.Fatalf("Iteration 3: Expected skip (target changed again), got %s", decision3.Action)
	}

	// Verify pending was updated again
	controller.stateMu.RLock()
	expectedPending3 := 25.5 // scheduled 25.0 + offset 0.7 = 25.7 → rounds to 25.5
	if state.PendingSetpoint != expectedPending3 {
		t.Errorf("Expected PendingSetpoint updated to %.1f, got %.1f", expectedPending3, state.PendingSetpoint)
	}
	controller.stateMu.RUnlock()

	t.Logf("✅ Iteration 3: Target changed again, updated pending=%.1f°C (not executed)", state.PendingSetpoint)

	t.Log("✅ FEEDBACK LOOP PREVENTED: Target kept changing, so never executed")
}

// TestDelayedExecution_NormalOperation verifies that delayed execution works correctly in normal scenarios
func TestDelayedExecution_NormalOperation(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                   true,
		DryRun:                    true,
		TemperatureThreshold:      0.2,
		OverrideDurationMinutes:   10,
		ExtensionThresholdMinutes: 2,
		MinSetpointCelsius:        10.0,
		MaxSetpointCelsius:        30.0,
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

	// Initialize room state
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:   "test-room-1",
		RoomName: "Bathroom",
	}
	controller.stateMu.Unlock()

	// Add sensor reading (23.0°C - room is cold)
	reading := &buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.SensorReading{
			Timestamp:   time.Now(),
			MAC:         "A4:C1:38:F1:2D:0D",
			TemperatureCelsius:23.0,
		},
	}
	controlBuffer.Add(reading)

	ctx := context.Background()

	// Iteration 1: Room cold, should mark pending
	roomStatusMap1 := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                       "test-room-1",
			Reachable:                true,
			ThermSetpointMode:        "schedule",
			ThermSetpointTemperature: 24.0, // Target 24°C
			ThermMeasuredTemperature: 24.5, // Thermostat sensor reads warm
			HeatingPowerRequest:      0,
		},
	}

	decision1 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap1)
	t.Logf("Iteration 1: action=%s, reason=%s", decision1.Action, decision1.Reason)

	if decision1.Action != "skip" {
		t.Fatalf("Iteration 1: Expected skip (marking pending), got %s", decision1.Action)
	}

	controller.stateMu.RLock()
	state := controller.stateByRoom["test-room-1"]
	pendingAfterIter1 := state.PendingSetpoint
	controller.stateMu.RUnlock()

	t.Logf("✅ Iteration 1: Marked pending=%.1f°C", pendingAfterIter1)

	// Iteration 2: Same condition (schedule stable) → should EXECUTE
	// Room status unchanged (schedule mode, same target)
	decision2 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap1)
	t.Logf("Iteration 2: action=%s, reason=%s", decision2.Action, decision2.Reason)

	if decision2.Action != "set_manual_override" {
		t.Fatalf("Iteration 2: Expected set_manual_override (confirmed), got %s: %s", decision2.Action, decision2.Reason)
	}

	// Verify pending was cleared
	controller.stateMu.RLock()
	if state.PendingSetpoint != 0 {
		t.Errorf("Expected PendingSetpoint to be cleared after execution, got %.1f", state.PendingSetpoint)
	}
	controller.stateMu.RUnlock()

	t.Logf("✅ Iteration 2: Confirmed and executed setpoint=%.1f°C", decision2.CalculatedSetpoint)
}

// TestDelayedExecution_TargetChanges verifies pending is updated when target changes
func TestDelayedExecution_TargetChanges(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                   true,
		DryRun:                    true,
		TemperatureThreshold:      0.2,
		OverrideDurationMinutes:   10,
		ExtensionThresholdMinutes: 2,
		MinSetpointCelsius:        10.0,
		MaxSetpointCelsius:        30.0,
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

	// Initialize room state
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:   "test-room-1",
		RoomName: "Bathroom",
	}
	controller.stateMu.Unlock()

	// Add sensor reading
	reading := &buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.SensorReading{
			Timestamp:   time.Now(),
			MAC:         "A4:C1:38:F1:2D:0D",
			TemperatureCelsius:23.0,
		},
	}
	controlBuffer.Add(reading)

	ctx := context.Background()

	// Iteration 1: Schedule target 24.0°C
	roomStatusMap1 := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                       "test-room-1",
			Reachable:                true,
			ThermSetpointMode:        "schedule",
			ThermSetpointTemperature: 24.0,
			ThermMeasuredTemperature: 24.5,
			HeatingPowerRequest:      0,
		},
	}

	decision1 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap1)
	t.Logf("Iteration 1: action=%s, pending=%.1f°C", decision1.Action, controller.stateByRoom["test-room-1"].PendingSetpoint)

	// Iteration 2: Schedule changes to 25.0°C (user changed schedule)
	roomStatusMap2 := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                       "test-room-1",
			Reachable:                true,
			ThermSetpointMode:        "schedule",
			ThermSetpointTemperature: 25.0, // Schedule changed!
			ThermMeasuredTemperature: 25.5,
			HeatingPowerRequest:      0,
		},
	}

	decision2 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap2)
	t.Logf("Iteration 2: action=%s, reason=%s", decision2.Action, decision2.Reason)

	// Should update pending, not execute
	if decision2.Action != "skip" {
		t.Errorf("Expected skip (target changed), got %s", decision2.Action)
	}

	controller.stateMu.RLock()
	state := controller.stateByRoom["test-room-1"]
	if state.PendingSetpoint == 0 {
		t.Error("Expected PendingSetpoint to be updated")
	}
	controller.stateMu.RUnlock()

	t.Logf("✅ Target changed, pending updated to %.1f°C", state.PendingSetpoint)

	// Iteration 3: Schedule stable at 25.0°C → should execute
	decision3 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap2)
	t.Logf("Iteration 3: action=%s", decision3.Action)

	if decision3.Action != "set_manual_override" {
		t.Errorf("Expected execution after stable target, got %s", decision3.Action)
	}

	t.Log("✅ After target stabilized, execution confirmed")
}

// TestDelayedExecution_ClearedWhenNoChangeNeeded verifies pending is cleared when no change needed
func TestDelayedExecution_ClearedWhenNoChangeNeeded(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                   true,
		DryRun:                    true,
		TemperatureThreshold:      0.2,
		OverrideDurationMinutes:   10,
		ExtensionThresholdMinutes: 2,
		MinSetpointCelsius:        10.0,
		MaxSetpointCelsius:        30.0,
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

	// Initialize room state with pending setpoint
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:              "test-room-1",
		RoomName:            "Bathroom",
		PendingSetpoint:     25.0,
		PendingSetpointTime: time.Now().Add(-3 * time.Minute),
	}
	controller.stateMu.Unlock()

	// Add sensor reading (room at target temperature)
	reading := &buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.SensorReading{
			Timestamp:   time.Now(),
			MAC:         "A4:C1:38:F1:2D:0D",
			TemperatureCelsius:24.0,
		},
	}
	controlBuffer.Add(reading)

	ctx := context.Background()

	// Room at target temperature, no change needed
	roomStatusMap := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                       "test-room-1",
			Reachable:                true,
			ThermSetpointMode:        "schedule",
			ThermSetpointTemperature: 24.0,
			ThermMeasuredTemperature: 24.0,
			HeatingPowerRequest:      0,
		},
	}

	decision := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap)
	t.Logf("Decision: action=%s, reason=%s", decision.Action, decision.Reason)

	if decision.Action != "no_adjustment_needed" {
		t.Errorf("Expected no_adjustment_needed, got %s", decision.Action)
	}

	// Verify pending was cleared
	controller.stateMu.RLock()
	state := controller.stateByRoom["test-room-1"]
	if state.PendingSetpoint != 0 {
		t.Errorf("Expected PendingSetpoint to be cleared, got %.1f", state.PendingSetpoint)
	}
	controller.stateMu.RUnlock()

	t.Log("✅ Pending cleared when no change needed")
}
