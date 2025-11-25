package control

import (
	"time"

	"github.com/mjasion/balena-home/thermostats/netatmo"
)

// ThermostatState tracks the state of a controlled thermostat
type ThermostatState struct {
	RoomID                   string
	RoomName                 string
	LastSetpoint             float64   // Last setpoint we commanded
	LastSetpointTime         time.Time // When we sent the last command
	OverrideEndTime          time.Time // When the current override expires
	ExternallyModified       bool      // Flag indicating manual override detected
	ExternalModificationTime time.Time // When external modification was detected
	SyncedScheduledTemp      float64   // Last synced scheduled temperature from Netatmo
	SyncedScheduledTime      time.Time // When we synced the scheduled temperature
	LastHomeMode             string    // Last known home mode (away, hg, etc.)
	LastManualSetpoint       float64   // Last manual setpoint observed from Netatmo
	LastManualEndTime        int64     // Last manual override end time observed from Netatmo

	// Runaway detection (backup safety layer)
	ConsecutiveIncreases   int       // Count of consecutive setpoint increases
	LastCalculatedSetpoint float64   // Last calculated setpoint (before safety limits)
	RunawayHaltUntil       time.Time // Control halted until this time (runaway protection)

	// Delayed execution (primary feedback loop prevention)
	PendingSetpoint     float64   // Setpoint calculated last iteration but not yet executed
	PendingSetpointTime time.Time // When the pending setpoint was calculated

	// Schedule sync flags
	ScheduleJustChanged bool // True when schedule changed during last sync, bypasses delayed execution once
}

// Copy creates a defensive copy of the state
func (s *ThermostatState) Copy() ThermostatState {
	return ThermostatState{
		RoomID:                   s.RoomID,
		RoomName:                 s.RoomName,
		LastSetpoint:             s.LastSetpoint,
		LastSetpointTime:         s.LastSetpointTime,
		OverrideEndTime:          s.OverrideEndTime,
		ExternallyModified:       s.ExternallyModified,
		ExternalModificationTime: s.ExternalModificationTime,
		SyncedScheduledTemp:      s.SyncedScheduledTemp,
		SyncedScheduledTime:      s.SyncedScheduledTime,
		LastHomeMode:             s.LastHomeMode,
		LastManualSetpoint:       s.LastManualSetpoint,
		LastManualEndTime:        s.LastManualEndTime,
		ConsecutiveIncreases:     s.ConsecutiveIncreases,
		LastCalculatedSetpoint:   s.LastCalculatedSetpoint,
		RunawayHaltUntil:         s.RunawayHaltUntil,
		PendingSetpoint:          s.PendingSetpoint,
		PendingSetpointTime:      s.PendingSetpointTime,
		ScheduleJustChanged:      s.ScheduleJustChanged,
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
	Action              string  // "skip", "no_adjustment_needed", "set_manual_override"
	Reason              string  // Human-readable reason for the action
	XiaomiTemperature   float64 // Weighted average from sensor
	ScheduledTemp       float64 // Target temperature from Netatmo schedule (when in "schedule" mode)
	SetpointTemperature float64 // Current setpoint temperature (could be schedule or manual override)
	ThermostatMeasured  float64 // Temperature reported by thermostat's built-in sensor
	ThermostatMode      string  // Thermostat mode: "schedule", "manual", "away", "hg" (frost guard), etc.
	CalculatedSetpoint  float64 // New setpoint (if action is set_manual_override)
	OverrideEndTime     int64   // Unix timestamp for override expiration
}

// CachedHomeStatus stores the latest home status with timestamp
type CachedHomeStatus struct {
	HomeStatus *netatmo.HomeStatusResponse
	FetchTime  time.Time
	FetchError error
}
