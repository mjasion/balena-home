package control

import (
	"math"
	"testing"
)

// TestSetpointCalculation tests the setpoint calculation logic with real scenarios
//
// Test Scenarios Summary:
// ┌────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
// │ Test Case                                    │ Xiaomi │ Thermo │ Schedule │ Threshold │ Setpoint │ Override │ Reason                │
// │                                              │  Temp  │  Temp  │   Temp   │           │          │ Needed?  │                       │
// ├──────────────────────────────────────────────┼────────┼────────┼──────────┼───────────┼──────────┼──────────┼───────────────────────┤
// │ Real scenario: Room too cold with offset     │ 23.6°C │ 25.0°C │  24.0°C  │   0.3°C   │  25.5°C  │   YES    │ Thermostat reads warm │
// │                                              │        │        │          │           │          │          │ but room is cold.     │
// │                                              │        │        │          │           │          │          │ Must override to      │
// │                                              │        │        │          │           │          │          │ trigger heating.      │
// ├──────────────────────────────────────────────┼────────┼────────┼──────────┼───────────┼──────────┼──────────┼───────────────────────┤
// │ Room too cold, no sensor offset              │ 18.5°C │ 19.2°C │  22.0°C  │   0.5°C   │  22.0°C  │    NO    │ Thermostat naturally  │
// │                                              │        │        │          │           │          │          │ heats (19.2 < 22.0)   │
// ├──────────────────────────────────────────────┼────────┼────────┼──────────┼───────────┼──────────┼──────────┼───────────────────────┤
// │ Room too warm, no intervention needed        │ 25.5°C │ 24.8°C │  22.0°C  │   0.5°C   │  22.0°C  │    NO    │ Thermostat naturally  │
// │                                              │        │        │          │           │          │          │ stops (24.8 > 22.0)   │
// ├──────────────────────────────────────────────┼────────┼────────┼──────────┼───────────┼──────────┼──────────┼───────────────────────┤
// │ Room too cold, large sensor offset           │ 20.0°C │ 24.0°C │  22.0°C  │   0.5°C   │  24.5°C  │   YES    │ Large offset requires │
// │                                              │        │        │          │           │          │          │ override to 24.5°C    │
// ├──────────────────────────────────────────────┼────────┼────────┼──────────┼───────────┼──────────┼──────────┼───────────────────────┤
// │ Room warm, thermostat reads cold (CRITICAL)  │ 24.0°C │ 21.0°C │  22.0°C  │   0.5°C   │  20.5°C  │   YES    │ Room warm but thermo  │
// │                                              │        │        │          │           │          │          │ reads cold. Must set  │
// │                                              │        │        │          │           │          │          │ 20.5°C to stop heat   │
// ├──────────────────────────────────────────────┼────────┼────────┼──────────┼───────────┼──────────┼──────────┼───────────────────────┤
// │ Room too warm, no override needed            │ 25.5°C │ 23.0°C │  22.0°C  │   0.5°C   │  22.0°C  │    NO    │ Setpoint matches      │
// │                                              │        │        │          │           │          │          │ schedule, thermo will │
// │                                              │        │        │          │           │          │          │ naturally stop        │
// ├──────────────────────────────────────────────┼────────┼────────┼──────────┼───────────┼──────────┼──────────┼───────────────────────┤
// │ Room too warm, large offset                  │ 26.0°C │ 25.0°C │  22.0°C  │   0.5°C   │  22.0°C  │    NO    │ Very warm room,       │
// │                                              │        │        │          │           │          │          │ schedule sufficient   │
// ├──────────────────────────────────────────────┼────────┼────────┼──────────┼───────────┼──────────┼──────────┼───────────────────────┤
// │ Within threshold - no action                 │ 24.2°C │ 24.5°C │  24.0°C  │   0.3°C   │   N/A    │    NO    │ Diff 0.2°C < 0.3°C    │
// │                                              │        │        │          │           │          │          │ threshold             │
// └──────────────────────────────────────────────┴────────┴────────┴──────────┴───────────┴──────────┴──────────┴───────────────────────┘
//
// Key Algorithm Logic:
//
//   - When room TOO COLD (xiaomi < scheduled):
//     calculatedSetpoint = max(scheduledTemp, thermostatMeasured + 0.5)
//     This ensures heating starts even if thermostat sensor reads warmer than reality
//
//   - When room TOO WARM (xiaomi > scheduled):
//     calculatedSetpoint = min(scheduledTemp, thermostatMeasured - 0.5)
//     This ensures heating stops even if thermostat sensor reads colder than reality
//
//   - Override is sent ONLY if: abs(calculatedSetpoint - scheduledTemp) >= 0.1°C
func TestSetpointCalculation(t *testing.T) {
	tests := []struct {
		name               string
		xiaomiTemp         float64
		scheduledTemp      float64
		thermostatMeasured float64
		threshold          float64
		expectedSetpoint   float64
		shouldOverride     bool
		description        string
	}{
		{
			name:               "Real scenario: Room too cold with sensor offset",
			xiaomiTemp:         23.6,
			scheduledTemp:      24.0,
			thermostatMeasured: 25.0,
			threshold:          0.3,
			expectedSetpoint:   25.5,
			shouldOverride:     true,
			description:        "Thermostat reads 25°C but room is actually 23.6°C. Need to override to 25.5°C to trigger heating.",
		},
		{
			name:               "Room too cold, no sensor offset",
			xiaomiTemp:         18.5,
			scheduledTemp:      22.0,
			thermostatMeasured: 19.2,
			threshold:          0.5,
			expectedSetpoint:   22.0,
			shouldOverride:     false,
			description:        "Thermostat will naturally heat since 19.2 < 22.0",
		},
		{
			name:               "Room too warm, no intervention needed",
			xiaomiTemp:         25.5,
			scheduledTemp:      22.0,
			thermostatMeasured: 24.8,
			threshold:          0.5,
			expectedSetpoint:   22.0,
			shouldOverride:     false,
			description:        "Thermostat will naturally stop heating since 24.8 > 22.0",
		},
		{
			name:               "Room too cold, large sensor offset",
			xiaomiTemp:         20.0,
			scheduledTemp:      22.0,
			thermostatMeasured: 24.0,
			threshold:          0.5,
			expectedSetpoint:   24.5,
			shouldOverride:     true,
			description:        "Large offset: need to set 24.5 to trigger heating when thermostat reads 24.0",
		},
		{
			name:               "Room too warm, thermostat reads cold - OVERRIDE NEEDED",
			xiaomiTemp:         24.0,
			scheduledTemp:      22.0,
			thermostatMeasured: 21.0,
			threshold:          0.5,
			expectedSetpoint:   20.5,
			shouldOverride:     true,
			description:        "Critical: Room warm (24°C) but thermostat reads cold (21°C). Must override to 20.5°C to stop heating.",
		},
		{
			name:               "Room too warm, no override needed",
			xiaomiTemp:         25.5,
			scheduledTemp:      22.0,
			thermostatMeasured: 23.0,
			threshold:          0.5,
			expectedSetpoint:   22.0,
			shouldOverride:     false,
			description:        "Room warm (25.5°C), thermostat reads 23.0°C. Setpoint min(22.0, 22.5) = 22.0, already scheduled",
		},
		{
			name:               "Room too warm with sensor offset (large)",
			xiaomiTemp:         26.0,
			scheduledTemp:      22.0,
			thermostatMeasured: 25.0,
			threshold:          0.5,
			expectedSetpoint:   22.0,
			shouldOverride:     false,
			description:        "Room very warm, setpoint min(22.0, 25.0-0.5) = 22.0, already scheduled",
		},
		{
			name:               "Within threshold - no action",
			xiaomiTemp:         24.2,
			scheduledTemp:      24.0,
			threshold:          0.3,
			thermostatMeasured: 24.5,
			expectedSetpoint:   0,
			shouldOverride:     false,
			description:        "Difference 0.2°C is below threshold 0.3°C",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate temperature difference
			tempDiff := tt.xiaomiTemp - tt.scheduledTemp

			// Check threshold
			if math.Abs(tempDiff) < tt.threshold {
				if tt.shouldOverride {
					t.Errorf("Expected override but difference %.2f°C is below threshold %.2f°C",
						math.Abs(tempDiff), tt.threshold)
				}
				return
			}

			// Calculate setpoint using the algorithm
			var calculatedSetpoint float64
			if tempDiff < 0 {
				// Room too cold
				calculatedSetpoint = math.Max(tt.scheduledTemp, tt.thermostatMeasured+0.5)
			} else {
				// Room too warm
				calculatedSetpoint = math.Min(tt.scheduledTemp, tt.thermostatMeasured-0.5)
			}

			// Check if setpoint matches expectation
			if math.Abs(calculatedSetpoint-tt.expectedSetpoint) > 0.01 {
				t.Errorf("Setpoint mismatch:\n  got: %.1f°C\n  want: %.1f°C\n  %s",
					calculatedSetpoint, tt.expectedSetpoint, tt.description)
			}

			// Check if override is needed
			needsOverride := math.Abs(calculatedSetpoint-tt.scheduledTemp) >= 0.1
			if needsOverride != tt.shouldOverride {
				t.Errorf("Override decision mismatch:\n  got: %v\n  want: %v\n  calculatedSetpoint: %.1f°C\n  scheduledTemp: %.1f°C\n  %s",
					needsOverride, tt.shouldOverride, calculatedSetpoint, tt.scheduledTemp, tt.description)
			}

			t.Logf("✓ %s\n  xiaomi=%.1f°C, scheduled=%.1f°C, thermostat=%.1f°C\n  → setpoint=%.1f°C, override=%v",
				tt.description, tt.xiaomiTemp, tt.scheduledTemp, tt.thermostatMeasured,
				calculatedSetpoint, needsOverride)
		})
	}
}
