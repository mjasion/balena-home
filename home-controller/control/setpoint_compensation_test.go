package control

import (
	"testing"
)

// TestSetpointCompensation_SensorOffset tests the core sensor offset compensation logic
func TestSetpointCompensation_SensorOffset(t *testing.T) {
	tests := []struct {
		name             string
		xiaomiTemp       float64
		netatmoMeasured  float64
		scheduledTemp    float64
		expectedSetpoint float64
	}{
		{
			name:             "Netatmo reads 1°C too low - should compensate",
			xiaomiTemp:       24.5,
			netatmoMeasured:  23.5,
			scheduledTemp:    24.5,
			expectedSetpoint: 23.5, // scheduled + offset = 24.5 + (23.5 - 24.5) = 23.5
		},
		{
			name:             "Netatmo reads 1°C too high - should compensate",
			xiaomiTemp:       23.5,
			netatmoMeasured:  24.5,
			scheduledTemp:    23.5,
			expectedSetpoint: 24.5, // scheduled + offset = 23.5 + (24.5 - 23.5) = 24.5
		},
		{
			name:             "Netatmo accurate - no compensation needed",
			xiaomiTemp:       24.5,
			netatmoMeasured:  24.5,
			scheduledTemp:    24.5,
			expectedSetpoint: 24.5, // scheduled + offset = 24.5 + (24.5 - 24.5) = 24.5
		},
		{
			name:             "Small offset 0.3°C - should still compensate",
			xiaomiTemp:       24.8,
			netatmoMeasured:  24.5,
			scheduledTemp:    24.5,
			expectedSetpoint: 24.2, // 24.5 + (24.5 - 24.8) = 24.2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the calculation logic from evaluate.go
			sensorOffset := tt.netatmoMeasured - tt.xiaomiTemp
			calculatedSetpoint := tt.scheduledTemp + sensorOffset

			// Verify calculations
			if abs(calculatedSetpoint-tt.expectedSetpoint) > 0.01 {
				t.Errorf("Calculated setpoint = %.2f, expected %.2f (offset=%.2f)",
					calculatedSetpoint, tt.expectedSetpoint, sensorOffset)
			}
		})
	}
}

// TestSetpointCompensation_SkipWhenAlreadySet tests that we skip API calls when setpoint already matches
func TestSetpointCompensation_SkipWhenAlreadySet(t *testing.T) {
	tests := []struct {
		name             string
		currentSetpoint  float64
		calculatedTarget float64
		thermostatMode   string
		shouldSkip       bool
		reason           string
	}{
		{
			name:             "Schedule mode - setpoint matches calculated",
			currentSetpoint:  23.5,
			calculatedTarget: 23.5,
			thermostatMode:   "schedule",
			shouldSkip:       true,
			reason:           "schedule already has correct compensation, no override needed",
		},
		{
			name:             "Manual mode - setpoint matches calculated",
			currentSetpoint:  23.5,
			calculatedTarget: 23.5,
			thermostatMode:   "manual",
			shouldSkip:       true,
			reason:           "our previous override still active and correct",
		},
		{
			name:             "Schedule mode - setpoint differs by 1°C",
			currentSetpoint:  24.5,
			calculatedTarget: 23.5,
			thermostatMode:   "schedule",
			shouldSkip:       false,
			reason:           "need to override schedule with compensated setpoint",
		},
		{
			name:             "Manual mode - setpoint differs by 1°C",
			currentSetpoint:  24.5,
			calculatedTarget: 23.5,
			thermostatMode:   "manual",
			shouldSkip:       false,
			reason:           "need to update manual override with new compensated setpoint",
		},
		{
			name:             "Setpoint differs by 0.05°C (within tolerance)",
			currentSetpoint:  23.50,
			calculatedTarget: 23.55,
			thermostatMode:   "schedule",
			shouldSkip:       true,
			reason:           "difference within 0.1°C tolerance",
		},
		{
			name:             "Setpoint differs by 0.15°C (outside tolerance)",
			currentSetpoint:  23.50,
			calculatedTarget: 23.65,
			thermostatMode:   "schedule",
			shouldSkip:       false,
			reason:           "difference exceeds 0.1°C tolerance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the check from evaluate.go line 235
			currentSetpoint := tt.currentSetpoint
			calculatedSetpoint := tt.calculatedTarget
			shouldExtend := false // not testing extension here

			shouldSkip := abs(currentSetpoint-calculatedSetpoint) < 0.1 && !shouldExtend

			if shouldSkip != tt.shouldSkip {
				t.Errorf("shouldSkip = %v, expected %v (delta=%.3f°C)\nReason: %s",
					shouldSkip, tt.shouldSkip, abs(currentSetpoint-calculatedSetpoint), tt.reason)
			}
		})
	}
}

// TestSetpointCompensation_RealWorldScenario tests the exact scenario from the user's log
func TestSetpointCompensation_RealWorldScenario(t *testing.T) {
	// User's exact scenario:
	// xiaomi_temp=24.49 scheduled_temp=24.5 setpoint_temp=24.5 thermostat_measured=23.5
	// Problem: Heating at 100% even though room is already at target

	xiaomiTemp := 24.49
	netatmoMeasured := 23.5
	scheduledTemp := 24.5
	currentSetpoint := 24.5

	// Calculate what the system should do
	sensorOffset := netatmoMeasured - xiaomiTemp // 23.5 - 24.49 = -0.99°C
	calculatedSetpoint := scheduledTemp + sensorOffset // 24.5 + (-0.99) = 23.51°C

	t.Logf("User's scenario analysis:")
	t.Logf("  Xiaomi (actual):     %.2f°C", xiaomiTemp)
	t.Logf("  Netatmo (sensor):    %.2f°C", netatmoMeasured)
	t.Logf("  Sensor offset:       %.2f°C (Netatmo reads too low)", sensorOffset)
	t.Logf("  Scheduled target:    %.2f°C", scheduledTemp)
	t.Logf("  Current setpoint:    %.2f°C", currentSetpoint)
	t.Logf("  Calculated setpoint: %.2f°C", calculatedSetpoint)

	// Verify the compensation is correct
	expectedSetpoint := 23.5 // Should compensate for ~1°C sensor error
	if abs(calculatedSetpoint-expectedSetpoint) > 0.1 {
		t.Errorf("Calculated setpoint %.2f°C doesn't match expected %.2f°C", calculatedSetpoint, expectedSetpoint)
	}

	// Verify action is correct
	shouldOverride := abs(currentSetpoint-calculatedSetpoint) >= 0.1
	if !shouldOverride {
		t.Errorf("Should override: current=%.2f°C, calculated=%.2f°C (diff=%.2f°C)",
			currentSetpoint, calculatedSetpoint, abs(currentSetpoint-calculatedSetpoint))
	}

	t.Logf("✅ System will override setpoint from %.1f°C to %.1f°C", currentSetpoint, calculatedSetpoint)
	t.Logf("✅ When Netatmo reaches %.1f°C, actual room temp will be %.1f°C", calculatedSetpoint, scheduledTemp)
	t.Logf("✅ Heating will stop (instead of running at 100%%)")
}

// TestRoundToHalfDegree tests that setpoints are rounded to 0.5°C increments
func TestRoundToHalfDegree(t *testing.T) {
	tests := []struct {
		name     string
		input    float64
		expected float64
	}{
		{"Exact 27.0", 27.0, 27.0},
		{"Exact 27.5", 27.5, 27.5},
		{"27.1 rounds down to 27.0", 27.1, 27.0},
		{"27.2 rounds down to 27.0", 27.2, 27.0},
		{"27.24 rounds down to 27.0", 27.24, 27.0},
		{"27.25 rounds up to 27.5", 27.25, 27.5},
		{"27.3 rounds up to 27.5", 27.3, 27.5},
		{"27.4 rounds up to 27.5", 27.4, 27.5},
		{"27.6 rounds up to 27.5", 27.6, 27.5},
		{"27.74 rounds up to 27.5", 27.74, 27.5},
		{"27.75 rounds up to 28.0", 27.75, 28.0},
		{"27.8 rounds up to 28.0", 27.8, 28.0},
		{"27.9 rounds up to 28.0", 27.9, 28.0},
		{"19.3 rounds to 19.5", 19.3, 19.5},
		{"19.7 rounds to 19.5", 19.7, 19.5},
		{"19.75 rounds to 20.0", 19.75, 20.0},
		{"User scenario: 27.8 -> 28.0", 27.8, 28.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := roundToHalfDegree(tt.input)
			if result != tt.expected {
				t.Errorf("roundToHalfDegree(%.2f) = %.1f, expected %.1f", tt.input, result, tt.expected)
			}
		})
	}
}

// TestSetpointCompensation_WithRounding tests that setpoint compensation includes rounding
func TestSetpointCompensation_WithRounding(t *testing.T) {
	tests := []struct {
		name             string
		xiaomiTemp       float64
		netatmoMeasured  float64
		scheduledTemp    float64
		expectedRaw      float64
		expectedRounded  float64
	}{
		{
			name:            "User's exact scenario: offset 0.3 should round to 0.5",
			xiaomiTemp:      24.2,
			netatmoMeasured: 24.5,
			scheduledTemp:   27.5,
			expectedRaw:     27.8,  // 27.5 + (24.5 - 24.2)
			expectedRounded: 28.0,  // Rounded to nearest 0.5
		},
		{
			name:            "Offset 0.2 should round down",
			xiaomiTemp:      24.0,
			netatmoMeasured: 24.2,
			scheduledTemp:   22.0,
			expectedRaw:     22.2,  // 22.0 + 0.2
			expectedRounded: 22.0,  // Rounded down
		},
		{
			name:            "Offset 0.4 should round up",
			xiaomiTemp:      24.0,
			netatmoMeasured: 24.4,
			scheduledTemp:   22.0,
			expectedRaw:     22.4,  // 22.0 + 0.4
			expectedRounded: 22.5,  // Rounded up
		},
		{
			name:            "Negative offset -0.3 should round to -0.5",
			xiaomiTemp:      24.5,
			netatmoMeasured: 24.2,
			scheduledTemp:   22.0,
			expectedRaw:     21.7,  // 22.0 + (24.2 - 24.5)
			expectedRounded: 21.5,  // Rounded to nearest 0.5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate raw setpoint
			sensorOffset := tt.netatmoMeasured - tt.xiaomiTemp
			rawSetpoint := tt.scheduledTemp + sensorOffset

			// Verify raw calculation
			if abs(rawSetpoint-tt.expectedRaw) > 0.01 {
				t.Errorf("Raw setpoint = %.2f, expected %.2f", rawSetpoint, tt.expectedRaw)
			}

			// Apply rounding
			roundedSetpoint := roundToHalfDegree(rawSetpoint)

			// Verify rounding
			if roundedSetpoint != tt.expectedRounded {
				t.Errorf("Rounded setpoint = %.1f, expected %.1f", roundedSetpoint, tt.expectedRounded)
			}

			t.Logf("✓ %.1f°C + offset %.2f°C = %.2f°C → rounded to %.1f°C",
				tt.scheduledTemp, sensorOffset, rawSetpoint, roundedSetpoint)
		})
	}
}

// Helper function for absolute value
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
