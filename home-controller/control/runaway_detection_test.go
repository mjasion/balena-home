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

// TestRunawayDetection verifies that runaway protection activates after 3 consecutive increases
//
// Test Scenario:
// - Simulates the actual runaway bug scenario from 2025-11-18
// - Algorithm calculates increasing setpoints 3 times in a row
// - On 3rd increase, runaway protection should halt control for 5 minutes
func TestRunawayDetection(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                  true,
		DryRun:                   true,
		TemperatureThreshold:     0.2,
		OverrideDurationMinutes:  10,
		ExtensionThresholdMinutes: 2,
		MinSetpointCelsius:       10.0,
		MaxSetpointCelsius:       30.0,
		Mappings: []config.ThermostatMapping{
			{
				RoomName:  "Bathroom",
				RoomID:    "test-room-1",
				SensorMAC: "A4:C1:38:F1:2D:0D",
			},
		},
	}

	// Create mock buffers
	controlBuffer := buffer.NewRingBuffer(100)
	metricsBuffer := buffer.NewRingBuffer(100)

	// Create mock Netatmo client (won't be called in dry-run)
	mockClient := &netatmo.Client{}

	controller := New(cfg, mockClient, controlBuffer, metricsBuffer, logger)
	controller.homeID = "test-home"

	// Initialize room state
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:                 "test-room-1",
		RoomName:               "Bathroom",
		ConsecutiveIncreases:   0,
		LastCalculatedSetpoint: 0,
	}
	controller.stateMu.Unlock()

	// Add mock sensor reading to control buffer (25.3°C - the actual value from the incident)
	reading := &buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.BLEReading{
			Timestamp:   time.Now(),
			MAC:         "A4:C1:38:F1:2D:0D",
			Temperature: 25.3,
		},
	}
	controlBuffer.Add(reading)

	ctx := context.Background()

	// Create room status map simulating the runaway scenario
	// Each iteration simulates the feedback loop where scheduled temp = previous setpoint

	// Iteration 1: scheduled=24.0, setpoint will be 24.5 (first increase)
	roomStatusMap1 := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                        "test-room-1",
			Name:                      "Bathroom",
			Reachable:                 true,
			ThermSetpointMode:         "schedule",
			ThermSetpointTemperature:  24.0, // This becomes the "scheduled" temp due to fallback bug
			ThermMeasuredTemperature:  26.0, // Netatmo sensor reads higher
			HeatingPowerRequest:       0,
		},
	}

	decision1 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap1)
	t.Logf("Iteration 1: action=%s, reason=%s", decision1.Action, decision1.Reason)

	if decision1.Action == "skip" {
		t.Fatalf("Iteration 1: Expected set_manual_override, got skip: %s", decision1.Reason)
	}

	// Verify consecutive increases counter = 1
	controller.stateMu.RLock()
	state := controller.stateByRoom["test-room-1"]
	if state.ConsecutiveIncreases != 1 {
		t.Errorf("Expected ConsecutiveIncreases=1, got %d", state.ConsecutiveIncreases)
	}
	controller.stateMu.RUnlock()

	// Iteration 2: scheduled=24.5 (from previous setpoint), setpoint will be 25.0 (second increase)
	roomStatusMap2 := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                        "test-room-1",
			Name:                      "Bathroom",
			Reachable:                 true,
			ThermSetpointMode:         "manual",
			ThermSetpointTemperature:  24.5, // Previous setpoint, used as "scheduled"
			ThermMeasuredTemperature:  26.0,
			HeatingPowerRequest:       0,
		},
	}

	// Update state to simulate last command sent
	controller.stateMu.Lock()
	state.LastSetpoint = 24.5
	state.LastSetpointTime = time.Now().Add(-2 * time.Minute) // Sent 2 minutes ago
	controller.stateMu.Unlock()

	decision2 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap2)
	t.Logf("Iteration 2: action=%s, reason=%s", decision2.Action, decision2.Reason)

	if decision2.Action == "skip" {
		t.Fatalf("Iteration 2: Expected set_manual_override, got skip: %s", decision2.Reason)
	}

	// Verify consecutive increases counter = 2
	controller.stateMu.RLock()
	if state.ConsecutiveIncreases != 2 {
		t.Errorf("Expected ConsecutiveIncreases=2, got %d", state.ConsecutiveIncreases)
	}
	controller.stateMu.RUnlock()

	// Iteration 3: scheduled=25.0 (from previous setpoint), setpoint will be 25.5 (THIRD INCREASE - RUNAWAY!)
	roomStatusMap3 := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                        "test-room-1",
			Name:                      "Bathroom",
			Reachable:                 true,
			ThermSetpointMode:         "manual",
			ThermSetpointTemperature:  25.0, // Previous setpoint, used as "scheduled"
			ThermMeasuredTemperature:  26.0,
			HeatingPowerRequest:       0,
		},
	}

	// Update state
	controller.stateMu.Lock()
	state.LastSetpoint = 25.0
	state.LastSetpointTime = time.Now().Add(-2 * time.Minute)
	controller.stateMu.Unlock()

	decision3 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap3)
	t.Logf("Iteration 3: action=%s, reason=%s", decision3.Action, decision3.Reason)

	// CRITICAL: On 3rd consecutive increase, control should be halted
	if decision3.Action != "skip" {
		t.Fatalf("Expected runaway protection to halt control (action=skip), got action=%s", decision3.Action)
	}

	// Verify reason contains "RUNAWAY DETECTED"
	if decision3.Reason == "" || decision3.Reason[:16] != "RUNAWAY DETECTED" {
		t.Errorf("Expected reason to start with 'RUNAWAY DETECTED', got: %s", decision3.Reason)
	}

	// Verify consecutive increases counter was reset to 0
	controller.stateMu.RLock()
	if state.ConsecutiveIncreases != 0 {
		t.Errorf("Expected ConsecutiveIncreases reset to 0 after runaway detected, got %d", state.ConsecutiveIncreases)
	}

	// Verify RunawayHaltUntil is set to ~5 minutes in future
	if state.RunawayHaltUntil.IsZero() {
		t.Error("Expected RunawayHaltUntil to be set, got zero time")
	}

	haltDuration := time.Until(state.RunawayHaltUntil)
	if haltDuration < 4*time.Minute || haltDuration > 6*time.Minute {
		t.Errorf("Expected halt duration ~5 minutes, got %v", haltDuration)
	}
	controller.stateMu.RUnlock()

	t.Logf("✅ Runaway protection activated: control halted for %v", haltDuration)

	// Iteration 4: Verify control remains halted during halt period
	decision4 := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap3)
	t.Logf("Iteration 4 (during halt): action=%s, reason=%s", decision4.Action, decision4.Reason)

	if decision4.Action != "skip" {
		t.Errorf("Expected control to remain halted (action=skip), got action=%s", decision4.Action)
	}

	if decision4.Reason[:19] != "runaway protection:" {
		t.Errorf("Expected reason to indicate runaway protection active, got: %s", decision4.Reason)
	}

	t.Log("✅ Control correctly halted during 5-minute protection window")
}

// TestRunawayDetectionReset verifies that consecutive increases counter resets when setpoint decreases or stays same
func TestRunawayDetectionReset(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                  true,
		DryRun:                   true,
		TemperatureThreshold:     0.2,
		OverrideDurationMinutes:  10,
		ExtensionThresholdMinutes: 2,
		MinSetpointCelsius:       10.0,
		MaxSetpointCelsius:       30.0,
		Mappings: []config.ThermostatMapping{
			{
				RoomName:  "Bathroom",
				RoomID:    "test-room-1",
				SensorMAC: "A4:C1:38:F1:2D:0D",
			},
		},
	}

	controlBuffer := buffer.NewRingBuffer(100)
	metricsBuffer := buffer.NewRingBuffer(100)
	mockClient := &netatmo.Client{}

	controller := New(cfg, mockClient, controlBuffer, metricsBuffer, logger)
	controller.homeID = "test-home"

	// Initialize room state with 2 consecutive increases
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:                 "test-room-1",
		RoomName:               "Bathroom",
		ConsecutiveIncreases:   2,
		LastCalculatedSetpoint: 25.0,
	}
	controller.stateMu.Unlock()

	// Add sensor reading showing room is warmer now (will cause setpoint to decrease)
	reading := &buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.BLEReading{
			Timestamp:   time.Now(),
			MAC:         "A4:C1:38:F1:2D:0D",
			Temperature: 24.5, // Warmer than scheduled (24.0)
		},
	}
	controlBuffer.Add(reading)

	ctx := context.Background()

	// Room status: scheduled=24.0, xiaomi=24.5 (room warmer than target)
	// Should calculate lower setpoint, resetting the counter
	roomStatusMap := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                        "test-room-1",
			Name:                      "Bathroom",
			Reachable:                 true,
			ThermSetpointMode:         "schedule",
			ThermSetpointTemperature:  24.0,
			ThermMeasuredTemperature:  24.2,
			HeatingPowerRequest:       0,
		},
	}

	decision := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap)
	t.Logf("Decision: action=%s, reason=%s", decision.Action, decision.Reason)

	// Verify consecutive increases counter was reset to 0
	controller.stateMu.RLock()
	state := controller.stateByRoom["test-room-1"]
	if state.ConsecutiveIncreases != 0 {
		t.Errorf("Expected ConsecutiveIncreases to reset to 0 when setpoint not increasing, got %d", state.ConsecutiveIncreases)
	}
	controller.stateMu.RUnlock()

	t.Log("✅ Consecutive increases counter correctly reset when setpoint not increasing")
}

// TestRunawayHaltExpiry verifies that control resumes after 5-minute halt period expires
func TestRunawayHaltExpiry(t *testing.T) {
	logger := zap.NewNop()

	cfg := &config.ThermostatControlConfig{
		Enabled:                  true,
		DryRun:                   true,
		TemperatureThreshold:     0.2,
		OverrideDurationMinutes:  10,
		ExtensionThresholdMinutes: 2,
		MinSetpointCelsius:       10.0,
		MaxSetpointCelsius:       30.0,
		Mappings: []config.ThermostatMapping{
			{
				RoomName:  "Bathroom",
				RoomID:    "test-room-1",
				SensorMAC: "A4:C1:38:F1:2D:0D",
			},
		},
	}

	controlBuffer := buffer.NewRingBuffer(100)
	metricsBuffer := buffer.NewRingBuffer(100)
	mockClient := &netatmo.Client{}

	controller := New(cfg, mockClient, controlBuffer, metricsBuffer, logger)
	controller.homeID = "test-home"

	// Initialize room state with halt that expired 1 minute ago
	controller.stateMu.Lock()
	controller.stateByRoom["test-room-1"] = &ThermostatState{
		RoomID:                 "test-room-1",
		RoomName:               "Bathroom",
		ConsecutiveIncreases:   0,
		LastCalculatedSetpoint: 0,
		RunawayHaltUntil:       time.Now().Add(-1 * time.Minute), // Expired 1 minute ago
	}
	controller.stateMu.Unlock()

	// Add sensor reading
	reading := &buffer.Reading{
		Type: buffer.ReadingTypeBLE,
		BLE: &buffer.BLEReading{
			Timestamp:   time.Now(),
			MAC:         "A4:C1:38:F1:2D:0D",
			Temperature: 23.0,
		},
	}
	controlBuffer.Add(reading)

	ctx := context.Background()

	roomStatusMap := map[string]*netatmo.RoomStatus{
		"test-room-1": {
			ID:                        "test-room-1",
			Name:                      "Bathroom",
			Reachable:                 true,
			ThermSetpointMode:         "schedule",
			ThermSetpointTemperature:  24.0,
			ThermMeasuredTemperature:  24.5,
			HeatingPowerRequest:       0,
		},
	}

	decision := controller.evaluateRoom(ctx, cfg.Mappings[0], roomStatusMap)
	t.Logf("Decision: action=%s, reason=%s", decision.Action, decision.Reason)

	// Control should resume (not skip due to halt)
	if decision.Reason[:19] == "runaway protection:" {
		t.Errorf("Expected control to resume after halt expiry, but halt still active: %s", decision.Reason)
	}

	t.Log("✅ Control correctly resumed after halt period expired")
}
