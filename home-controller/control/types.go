package control

import (
	"time"
)

// ThermostatState tracks the state of a controlled thermostat
type ThermostatState struct {
	RoomID                   string
	RoomName                 string
	LastSetpoint             float64   // Last setpoint we commanded
	LastSetpointTime         time.Time // When we sent the last command
	OverrideEndTime          time.Time // When the current override expires
	OriginalScheduledTemp    float64   // Original schedule temperature when override was sent
	ScheduleEndTime          time.Time // When the current schedule period ends
	ManualModeSince          time.Time // When thermostat entered manual mode (for takeover detection)
	ExternallyModified       bool      // Flag indicating manual override detected
	ExternalModificationTime time.Time // When external modification was detected
}

// Copy creates a defensive copy of the state
func (s *ThermostatState) Copy() ThermostatState {
	return ThermostatState{
		RoomID:                   s.RoomID,
		RoomName:                 s.RoomName,
		LastSetpoint:             s.LastSetpoint,
		LastSetpointTime:         s.LastSetpointTime,
		OverrideEndTime:          s.OverrideEndTime,
		OriginalScheduledTemp:    s.OriginalScheduledTemp,
		ScheduleEndTime:          s.ScheduleEndTime,
		ManualModeSince:          s.ManualModeSince,
		ExternallyModified:       s.ExternallyModified,
		ExternalModificationTime: s.ExternalModificationTime,
	}
}

// SensorReading represents a BLE sensor temperature reading
type SensorReading struct {
	Timestamp   time.Time
	Temperature float64
}

// ControlDecision represents a decision made by the control algorithm
type ControlDecision struct {
	RoomID              string
	RoomName            string
	Action              string  // "skip", "no_adjustment_needed", "set_manual_override", "cancel_override"
	Reason              string  // Human-readable reason for the action
	XiaomiTemperature   float64 // Weighted average from sensor
	ScheduledTemp       float64 // Target temperature from Netatmo schedule (when in "schedule" mode)
	SetpointTemperature float64 // Current setpoint temperature (could be schedule or manual override)
	ThermostatMeasured  float64 // Temperature reported by thermostat's built-in sensor
	ThermostatMode      string  // Thermostat mode: "schedule", "manual", "away", "hg" (frost guard), etc.
	CalculatedSetpoint  float64 // New setpoint (if action is set_manual_override)
	OverrideEndTime     int64   // Unix timestamp for override expiration
	ScheduleEndTime     int64   // Unix timestamp for when schedule period ends
}
