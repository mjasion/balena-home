// Package control provides thermostat control logic for the home-controller service.
//
// This package implements an intelligent climate control system that:
//   - Monitors BLE temperature sensors (Xiaomi LYWSD03MMC)
//   - Controls Netatmo thermostats via API
//   - Compensates for sensor placement differences
//   - Respects user manual overrides
//   - Syncs with Netatmo schedules periodically
//
// The control algorithm applies sensor offset compensation to ensure accurate
// temperature control based on external sensors rather than thermostat-internal sensors.
package control

import "time"

// Temperature constants
const (
	// SetpointToleranceCelsius is the tolerance for comparing setpoints (within 0.1°C is considered equal)
	SetpointToleranceCelsius = 0.1

	// ManualSetpointToleranceCelsius is the tolerance for detecting if a manual override is ours (within 0.3°C)
	ManualSetpointToleranceCelsius = 0.3

	// MinAbsoluteSetpointCelsius is the absolute minimum setpoint to prevent freezing
	MinAbsoluteSetpointCelsius = 7.0

	// MaxAbsoluteSetpointCelsius is the absolute maximum setpoint to prevent overheating
	MaxAbsoluteSetpointCelsius = 30.0
)

// Time constants
const (
	// MinTimeSinceCommandForDetection is the minimum time to wait before detecting external modifications
	// (allows API propagation time)
	MinTimeSinceCommandForDetection = 2 * time.Minute

	// MaxTimeForOurOverride is the maximum time since we sent a command to still consider an override as ours
	MaxTimeForOurOverride = 15 * time.Minute

	// MaxSyncedScheduleTempAge is the maximum age for synced schedule temperature to be considered fresh
	MaxSyncedScheduleTempAge = 1 * time.Hour

	// SensorReadingWindowSeconds is the time window for collecting sensor readings
	SensorReadingWindowSeconds = 60
)
