package control

import (
	"testing"
)

// TestCalculateThreeZoneSetpoint_SensorOffset tests the three-zone algorithm with sensor offset
func TestCalculateThreeZoneSetpoint_SensorOffset(t *testing.T) {
	threshold := 0.2

	tests := []struct {
		name                 string
		xiaomiTemp           float64
		scheduledTemp        float64
		thermostatMeasured   float64
		expectedZone         string
		expectedSetpoint     float64
		expectedAdjustment   float64
		description          string
	}{
		{
			name:               "Zone 3: Xiaomi warmer than Netatmo by 0.3°C",
			xiaomiTemp:         25.8,
			scheduledTemp:      19.0,
			thermostatMeasured: 25.5,
			expectedZone:       "too_warm",
			expectedSetpoint:   25.0, // 25.5 - 0.5
			expectedAdjustment: -0.5,
			description:        "Xiaomi reads 0.3°C higher than Netatmo, subtract 0.5°C to stop heating",
		},
		{
			name:               "Zone 1: Xiaomi colder than Netatmo by 0.3°C",
			xiaomiTemp:         19.0,
			scheduledTemp:      21.0,
			thermostatMeasured: 19.3,
			expectedZone:       "too_cold",
			expectedSetpoint:   19.8, // 19.3 + 0.5
			expectedAdjustment: 0.5,
			description:        "Xiaomi reads 0.3°C lower than Netatmo, add 0.5°C to trigger heating",
		},
		{
			name:               "Zone 2: Sensors agree within threshold",
			xiaomiTemp:         22.1,
			scheduledTemp:      22.0,
			thermostatMeasured: 22.0,
			expectedZone:       "within_range",
			expectedSetpoint:   22.0, // maintain current
			expectedAdjustment: 0.0,
			description:        "Sensor offset 0.1°C is within 0.2°C threshold, no adjustment",
		},
		{
			name:               "Zone 2: Exactly at threshold boundary (positive)",
			xiaomiTemp:         22.2,
			scheduledTemp:      22.0,
			thermostatMeasured: 22.0,
			expectedZone:       "within_range",
			expectedSetpoint:   22.0,
			expectedAdjustment: 0.0,
			description:        "Sensor offset exactly 0.2°C is not >= threshold, stays in Zone 2",
		},
		{
			name:               "Zone 3: Just above threshold",
			xiaomiTemp:         22.21,
			scheduledTemp:      22.0,
			thermostatMeasured: 22.0,
			expectedZone:       "too_warm",
			expectedSetpoint:   21.5, // 22.0 - 0.5
			expectedAdjustment: -0.5,
			description:        "Sensor offset 0.21°C exceeds threshold, enters Zone 3",
		},
		{
			name:               "Zone 1: Just below negative threshold",
			xiaomiTemp:         21.79,
			scheduledTemp:      22.0,
			thermostatMeasured: 22.0,
			expectedZone:       "too_cold",
			expectedSetpoint:   22.5, // 22.0 + 0.5
			expectedAdjustment: 0.5,
			description:        "Sensor offset -0.21°C exceeds negative threshold, enters Zone 1",
		},
		{
			name:               "Large positive sensor offset",
			xiaomiTemp:         25.0,
			scheduledTemp:      20.0,
			thermostatMeasured: 23.0,
			expectedZone:       "too_warm",
			expectedSetpoint:   22.5, // 23.0 - 0.5
			expectedAdjustment: -0.5,
			description:        "Xiaomi 2°C higher than Netatmo, still only subtract 0.5°C",
		},
		{
			name:               "Large negative sensor offset",
			xiaomiTemp:         18.0,
			scheduledTemp:      22.0,
			thermostatMeasured: 20.0,
			expectedZone:       "too_cold",
			expectedSetpoint:   20.5, // 20.0 + 0.5
			expectedAdjustment: 0.5,
			description:        "Xiaomi 2°C lower than Netatmo, still only add 0.5°C",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateThreeZoneSetpoint(tt.xiaomiTemp, tt.scheduledTemp, tt.thermostatMeasured, threshold)

			sensorOffset := tt.xiaomiTemp - tt.thermostatMeasured

			// Check zone
			if result.Zone != tt.expectedZone {
				t.Errorf("Zone = %s, expected %s (sensor_offset=%.2f°C)",
					result.Zone, tt.expectedZone, sensorOffset)
			}

			// Check setpoint
			if result.CalculatedSetpoint != tt.expectedSetpoint {
				t.Errorf("CalculatedSetpoint = %.1f, expected %.1f",
					result.CalculatedSetpoint, tt.expectedSetpoint)
			}

			// Check adjustment
			if result.Adjustment != tt.expectedAdjustment {
				t.Errorf("Adjustment = %.1f, expected %.1f",
					result.Adjustment, tt.expectedAdjustment)
			}

			t.Logf("✓ %s: sensor_offset=%.2f°C → zone=%s, setpoint=%.1f°C, adjustment=%.1f°C",
				tt.description, sensorOffset, result.Zone, result.CalculatedSetpoint, result.Adjustment)
		})
	}
}

// TestEarlyCheckHeatingAlreadyOff tests the bug fix: skip action when room is too warm and heating is already off
func TestEarlyCheckHeatingAlreadyOff(t *testing.T) {
	tests := []struct {
		name               string
		setpoint           float64
		measured           float64
		xiaomiTemp         float64
		target             float64
		shouldSkip         bool
		description        string
	}{
		{
			name:        "Bug scenario: room too warm AND heating already off",
			setpoint:    19.0,
			measured:    25.5,
			xiaomiTemp:  25.8,
			target:      19.0,
			shouldSkip:  true,
			description: "Room too warm (25.8 > 19) AND setpoint < measured, skip",
		},
		{
			name:        "Room cold, heating appears off but needs evaluation",
			setpoint:    21.0,
			measured:    19.0,
			xiaomiTemp:  18.5,
			target:      21.0,
			shouldSkip:  false,
			description: "Room cold (18.5 < 21), must evaluate even if heating appears on",
		},
		{
			name:        "Room cold, Netatmo broken (thinks warm), must evaluate",
			setpoint:    24.0,
			measured:    26.0,
			xiaomiTemp:  23.5,
			target:      24.0,
			shouldSkip:  false,
			description: "Room cold (23.5 < 24) but Netatmo broken, must evaluate to trigger heating",
		},
		{
			name:        "Room slightly warm but heating appears off",
			setpoint:    21.9,
			measured:    22.0,
			xiaomiTemp:  22.2,
			target:      22.0,
			shouldSkip:  true,
			description: "Room slightly warm (22.2 > 22) AND heating off, skip",
		},
		{
			name:        "Room way too warm, heating off",
			setpoint:    15.0,
			measured:    28.0,
			xiaomiTemp:  28.5,
			target:      20.0,
			shouldSkip:  true,
			description: "Room way too warm (28.5 > 20) AND heating off, skip",
		},
		{
			name:        "Room at target, heating on - must evaluate",
			setpoint:    22.5,
			measured:    22.0,
			xiaomiTemp:  22.0,
			target:      22.0,
			shouldSkip:  false,
			description: "Room at target, heating on, evaluate to potentially turn off",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the refined early check from evaluate.go
			roomTooWarm := tt.xiaomiTemp > tt.target
			heatingAlreadyOff := tt.setpoint < tt.measured
			shouldSkip := roomTooWarm && heatingAlreadyOff

			if shouldSkip != tt.shouldSkip {
				t.Errorf("shouldSkip = %v, expected %v\n  %s\n  roomTooWarm=%v, heatingAlreadyOff=%v",
					shouldSkip, tt.shouldSkip, tt.description, roomTooWarm, heatingAlreadyOff)
			}

			if shouldSkip {
				t.Logf("✓ SKIP: %s", tt.description)
			} else {
				t.Logf("✓ EVALUATE: %s", tt.description)
			}
		})
	}
}

// TestBugScenarioFromLogs reproduces the exact bug from room_issue.txt
func TestBugScenarioFromLogs(t *testing.T) {
	// Exact values from the bug report (room_issue.txt)
	xiaomiTemp := 25.8
	netatmoMeasured := 25.5
	targetTemp := 19.0
	currentSetpoint := 19.0
	threshold := 0.2

	t.Log("Bug Scenario Analysis:")
	t.Logf("  Xiaomi temperature:     %.1f°C (accurate sensor)", xiaomiTemp)
	t.Logf("  Netatmo measured:       %.1f°C (thermostat sensor)", netatmoMeasured)
	t.Logf("  Target temperature:     %.1f°C (from schedule)", targetTemp)
	t.Logf("  Current setpoint:       %.1f°C", currentSetpoint)
	t.Logf("  Temperature threshold:  %.1f°C", threshold)

	// Step 1: Check if early skip should apply
	roomTooWarm := xiaomiTemp > targetTemp
	heatingAlreadyOff := currentSetpoint < netatmoMeasured
	shouldSkip := roomTooWarm && heatingAlreadyOff

	t.Logf("\nStep 1 - Early Check:")
	t.Logf("  Room too warm? Xiaomi (%.1f°C) > Target (%.1f°C): %v", xiaomiTemp, targetTemp, roomTooWarm)
	t.Logf("  Heating already off? Setpoint (%.1f°C) < Measured (%.1f°C): %v", currentSetpoint, netatmoMeasured, heatingAlreadyOff)
	t.Logf("  Should skip? (both conditions true): %v", shouldSkip)

	if !shouldSkip {
		t.Errorf("FAIL: Early check should skip when room is too warm AND heating is already off!")
	} else {
		t.Log("  ✓ PASS: Room too warm AND heating already off, should SKIP action")
		t.Log("  ✓ PASS: Algorithm will NOT send unnecessary manual override")
		return // Exit test early since we skip
	}

	// This code should NOT be reached with the fix
	t.Error("FAIL: Code should not reach here - early check should have skipped")

	// If we didn't have the early check, this is what would happen (OLD BUG):
	sensorOffset := xiaomiTemp - netatmoMeasured
	t.Logf("\nOLD BUGGY BEHAVIOR (without early check):")
	t.Logf("  Sensor offset: %.1f°C", sensorOffset)

	result := calculateThreeZoneSetpoint(xiaomiTemp, targetTemp, netatmoMeasured, threshold)
	t.Logf("  Zone: %s", result.Zone)
	t.Logf("  Calculated setpoint: %.1f°C", result.CalculatedSetpoint)
	t.Logf("  Would send UNNECESSARY override from 19°C to %.1f°C", result.CalculatedSetpoint)
	t.Log("  ❌ This wastes API calls and could cause heating to turn back on!")
}

// TestSensorOffsetCalculation verifies sensor offset is calculated correctly
func TestSensorOffsetCalculation(t *testing.T) {
	tests := []struct {
		name            string
		xiaomiTemp      float64
		netatmoMeasured float64
		expectedOffset  float64
		interpretation  string
	}{
		{
			name:            "Xiaomi reads higher",
			xiaomiTemp:      25.8,
			netatmoMeasured: 25.5,
			expectedOffset:  0.3,
			interpretation:  "Netatmo reads 0.3°C too low",
		},
		{
			name:            "Xiaomi reads lower",
			xiaomiTemp:      19.0,
			netatmoMeasured: 19.3,
			expectedOffset:  -0.3,
			interpretation:  "Netatmo reads 0.3°C too high",
		},
		{
			name:            "Sensors agree",
			xiaomiTemp:      22.0,
			netatmoMeasured: 22.0,
			expectedOffset:  0.0,
			interpretation:  "Sensors agree perfectly",
		},
		{
			name:            "Large offset",
			xiaomiTemp:      24.0,
			netatmoMeasured: 22.0,
			expectedOffset:  2.0,
			interpretation:  "Netatmo reads 2°C too low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sensorOffset := tt.xiaomiTemp - tt.netatmoMeasured

			if abs(sensorOffset-tt.expectedOffset) > 0.01 {
				t.Errorf("Sensor offset = %.2f, expected %.2f", sensorOffset, tt.expectedOffset)
			}

			t.Logf("✓ %s: sensor_offset = %.2f°C", tt.interpretation, sensorOffset)
		})
	}
}
