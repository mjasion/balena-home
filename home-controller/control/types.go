package control

import (
	"sync"
	"time"

	"github.com/mjasion/balena-home/thermostats/netatmo"
)

// ThermostatState tracks the minimal state needed for controlled thermostat
type ThermostatState struct {
	RoomID                   string    // Room ID from Netatmo
	RoomName                 string    // Room name for logging
	LastHomeMode             string    // Last known home mode (away, hg, schedule, manual)
	ExternallyModified       bool      // Flag indicating human override detected
	ExternalModificationTime time.Time // When external modification was detected
}

// SharedHomeStatus holds the latest home status data with thread-safe access
// Used for communication between Metric Job and Control Job
type SharedHomeStatus struct {
	mu               sync.RWMutex
	homeStatus       *netatmo.HomeStatusResponse
	fetchedAt        time.Time // When the data was fetched (local system time)
	metricJobTraceID string    // Trace ID from Metric Job for correlation
}

// NewSharedHomeStatus creates a new shared home status container
func NewSharedHomeStatus() *SharedHomeStatus {
	return &SharedHomeStatus{}
}

// Set stores new home status data (called by Metric Job)
func (s *SharedHomeStatus) Set(homeStatus *netatmo.HomeStatusResponse, traceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.homeStatus = homeStatus
	s.fetchedAt = time.Now()
	s.metricJobTraceID = traceID
}

// Get retrieves the current home status data with its age
// Returns: homeStatus, age, metricJobTraceID, hasData
func (s *SharedHomeStatus) Get() (*netatmo.HomeStatusResponse, time.Duration, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.homeStatus == nil {
		return nil, 0, "", false
	}

	age := time.Since(s.fetchedAt)
	return s.homeStatus, age, s.metricJobTraceID, true
}

// Copy creates a defensive copy of the state
func (s *ThermostatState) Copy() ThermostatState {
	return ThermostatState{
		RoomID:                   s.RoomID,
		RoomName:                 s.RoomName,
		LastHomeMode:             s.LastHomeMode,
		ExternallyModified:       s.ExternallyModified,
		ExternalModificationTime: s.ExternalModificationTime,
	}
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
