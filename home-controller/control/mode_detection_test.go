package control

import (
	"testing"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.uber.org/zap"
)

func TestDetectHomeModeChange(t *testing.T) {
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

	c := New(cfg, nil, controlBuffer, metricsBuffer, logger)
	c.stateByRoom["room1"] = &ThermostatState{
		RoomID:   "room1",
		RoomName: "Living Room",
	}

	tests := []struct {
		name              string
		initialMode       string
		currentMode       string
		expectChange      bool
		expectFlagCleared bool
	}{
		{
			name:              "First time seeing room - no change",
			initialMode:       "",
			currentMode:       "schedule",
			expectChange:      false,
			expectFlagCleared: false,
		},
		{
			name:              "Normal to away - should reset",
			initialMode:       "schedule",
			currentMode:       "away",
			expectChange:      true,
			expectFlagCleared: true,
		},
		{
			name:              "Normal to hg - should reset",
			initialMode:       "manual",
			currentMode:       "hg",
			expectChange:      true,
			expectFlagCleared: true,
		},
		{
			name:              "Away to normal - should reset",
			initialMode:       "away",
			currentMode:       "schedule",
			expectChange:      true,
			expectFlagCleared: true,
		},
		{
			name:              "HG to normal - should reset",
			initialMode:       "hg",
			currentMode:       "manual",
			expectChange:      true,
			expectFlagCleared: true,
		},
		{
			name:              "Manual to schedule - no reset",
			initialMode:       "manual",
			currentMode:       "schedule",
			expectChange:      false,
			expectFlagCleared: false,
		},
		{
			name:              "Schedule to manual - no reset",
			initialMode:       "schedule",
			currentMode:       "manual",
			expectChange:      false,
			expectFlagCleared: false,
		},
		{
			name:              "Away to hg - no reset",
			initialMode:       "away",
			currentMode:       "hg",
			expectChange:      false,
			expectFlagCleared: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset state
			c.stateByRoom["room1"] = &ThermostatState{
				RoomID:             "room1",
				RoomName:           "Living Room",
				LastHomeMode:       tt.initialMode,
				ExternallyModified: true, // Set flag to test if it gets cleared
			}

			roomStatus := &netatmo.RoomStatus{
				ID:                   "room1",
				ThermSetpointMode:    tt.currentMode,
				ThermSetpointEndTime: 0,
			}

			state := c.stateByRoom["room1"].Copy()
			changed := c.detectHomeModeChange(&state, roomStatus)

			if changed != tt.expectChange {
				t.Errorf("Expected change=%v, got %v", tt.expectChange, changed)
			}

			// Check if external modification flag was cleared
			if tt.expectFlagCleared {
				if c.stateByRoom["room1"].ExternallyModified {
					t.Error("Expected external modification flag to be cleared")
				}
			}

			// Verify mode was updated
			if tt.initialMode != "" {
				if c.stateByRoom["room1"].LastHomeMode != tt.currentMode {
					t.Errorf("Expected LastHomeMode=%s, got %s", tt.currentMode, c.stateByRoom["room1"].LastHomeMode)
				}
			}
		})
	}
}

func TestDetectExternalManualChange(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:              true,
		TemperatureThreshold: 0.5,
	}

	c := New(cfg, nil, controlBuffer, metricsBuffer, logger)
	c.stateByRoom["room1"] = &ThermostatState{
		RoomID:   "room1",
		RoomName: "Living Room",
	}

	now := time.Now()

	tests := []struct {
		name                   string
		thermostatMode         string
		lastSetpointTime       time.Time
		lastManualSetpoint     float64
		lastManualEndTime      int64
		currentSetpoint        float64
		currentEndTime         int64
		expectExternalModified bool
	}{
		{
			name:                   "Not manual mode - no detection",
			thermostatMode:         "schedule",
			lastSetpointTime:       now.Add(-5 * time.Minute),
			lastManualSetpoint:     22.0,
			currentSetpoint:        25.0,
			expectExternalModified: false,
		},
		{
			name:                   "No previous commands - store baseline",
			thermostatMode:         "manual",
			lastSetpointTime:       time.Time{},
			currentSetpoint:        22.0,
			currentEndTime:         now.Add(10 * time.Minute).Unix(),
			expectExternalModified: false,
		},
		{
			name:                   "Within grace period - no detection",
			thermostatMode:         "manual",
			lastSetpointTime:       now.Add(-1 * time.Minute),
			lastManualSetpoint:     22.0,
			currentSetpoint:        25.0,
			expectExternalModified: false,
		},
		{
			name:                   "Setpoint changed externally",
			thermostatMode:         "manual",
			lastSetpointTime:       now.Add(-3 * time.Minute),
			lastManualSetpoint:     22.0,
			lastManualEndTime:      now.Add(10 * time.Minute).Unix(),
			currentSetpoint:        25.0,
			currentEndTime:         now.Add(10 * time.Minute).Unix(),
			expectExternalModified: true,
		},
		{
			name:                   "End time changed externally",
			thermostatMode:         "manual",
			lastSetpointTime:       now.Add(-3 * time.Minute),
			lastManualSetpoint:     22.0,
			lastManualEndTime:      now.Add(10 * time.Minute).Unix(),
			currentSetpoint:        22.0,
			currentEndTime:         now.Add(20 * time.Minute).Unix(),
			expectExternalModified: true,
		},
		{
			name:                   "Small setpoint change - no detection",
			thermostatMode:         "manual",
			lastSetpointTime:       now.Add(-3 * time.Minute),
			lastManualSetpoint:     22.0,
			lastManualEndTime:      now.Add(10 * time.Minute).Unix(),
			currentSetpoint:        22.05,
			currentEndTime:         now.Add(10 * time.Minute).Unix(),
			expectExternalModified: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.stateByRoom["room1"] = &ThermostatState{
				RoomID:             "room1",
				RoomName:           "Living Room",
				LastSetpointTime:   tt.lastSetpointTime,
				LastManualSetpoint: tt.lastManualSetpoint,
				LastManualEndTime:  tt.lastManualEndTime,
			}

			roomStatus := &netatmo.RoomStatus{
				ID:                       "room1",
				ThermSetpointMode:        tt.thermostatMode,
				ThermSetpointTemperature: tt.currentSetpoint,
				ThermSetpointEndTime:     tt.currentEndTime,
			}

			state := c.stateByRoom["room1"].Copy()
			detected := c.detectExternalManualChange(&state, roomStatus)

			if detected != tt.expectExternalModified {
				t.Errorf("Expected external modified=%v, got %v", tt.expectExternalModified, detected)
			}

			if tt.expectExternalModified {
				if !c.stateByRoom["room1"].ExternallyModified {
					t.Error("Expected ExternallyModified flag to be set")
				}
			}
		})
	}
}

func TestShouldControlRoom(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	controlBuffer := buffer.New(100, logger)
	metricsBuffer := buffer.New(100, logger)

	cfg := &config.ThermostatControlConfig{
		Enabled:              true,
		TemperatureThreshold: 0.5,
	}

	c := New(cfg, nil, controlBuffer, metricsBuffer, logger)

	now := time.Now()

	tests := []struct {
		name             string
		thermostatMode   string
		endTime          int64
		lastSetpointTime time.Time
		lastSetpoint     float64
		currentSetpoint  float64
		externallyMod    bool
		expectControl    bool
		expectedReason   string
	}{
		{
			name:           "Schedule mode - allow control",
			thermostatMode: "schedule",
			expectControl:  true,
			expectedReason: "",
		},
		{
			name:             "Manual mode set by algorithm recently - allow control",
			thermostatMode:   "manual",
			lastSetpointTime: now.Add(-5 * time.Minute),
			lastSetpoint:     22.0,
			currentSetpoint:  22.0,
			expectControl:    true,
			expectedReason:   "",
		},
		{
			name:             "Manual mode set by algorithm but setpoint differs - skip",
			thermostatMode:   "manual",
			lastSetpointTime: now.Add(-5 * time.Minute),
			lastSetpoint:     22.0,
			currentSetpoint:  25.0,
			expectControl:    false,
			expectedReason:   "manual mode not set by algorithm",
		},
		{
			name:             "Manual mode set long ago - skip",
			thermostatMode:   "manual",
			lastSetpointTime: now.Add(-20 * time.Minute),
			lastSetpoint:     22.0,
			currentSetpoint:  22.0,
			expectControl:    false,
			expectedReason:   "manual mode not set by algorithm",
		},
		{
			name:           "Manual mode externally modified - skip",
			thermostatMode: "manual",
			externallyMod:  true,
			expectControl:  false,
			expectedReason: "externally modified manual override, respecting user intent",
		},
		{
			name:           "Away mode without manual override - allow control",
			thermostatMode: "away",
			endTime:        0,
			expectControl:  true,
			expectedReason: "",
		},
		{
			name:           "Away mode with manual override - skip",
			thermostatMode: "away",
			endTime:        now.Add(10 * time.Minute).Unix(),
			expectControl:  false,
			expectedReason: "manual override on top of away mode",
		},
		{
			name:           "HG mode without manual override - allow control",
			thermostatMode: "hg",
			endTime:        0,
			expectControl:  true,
			expectedReason: "",
		},
		{
			name:           "HG mode with manual override - skip",
			thermostatMode: "hg",
			endTime:        now.Add(10 * time.Minute).Unix(),
			expectControl:  false,
			expectedReason: "manual override on top of hg mode",
		},
		{
			name:           "Unknown mode - skip",
			thermostatMode: "unknown",
			expectControl:  false,
			expectedReason: "unknown thermostat mode: unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &ThermostatState{
				RoomID:             "room1",
				RoomName:           "Living Room",
				LastSetpointTime:   tt.lastSetpointTime,
				LastSetpoint:       tt.lastSetpoint,
				ExternallyModified: tt.externallyMod,
			}

			roomStatus := &netatmo.RoomStatus{
				ID:                       "room1",
				ThermSetpointMode:        tt.thermostatMode,
				ThermSetpointTemperature: tt.currentSetpoint,
				ThermSetpointEndTime:     tt.endTime,
			}

			shouldControl, reason := c.shouldControlRoom(state, roomStatus)

			if shouldControl != tt.expectControl {
				t.Errorf("Expected shouldControl=%v, got %v", tt.expectControl, shouldControl)
			}

			if !shouldControl && reason != tt.expectedReason {
				t.Errorf("Expected reason='%s', got '%s'", tt.expectedReason, reason)
			}
		})
	}
}
