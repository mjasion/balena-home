package control

import (
	"github.com/mjasion/balena-home/thermostats/netatmo"
)

// isHumanOverride checks if the current manual override was set by a human
// Returns true if override duration is >= 60 minutes
func (c *Controller) isHumanOverride(roomStatus *netatmo.RoomStatus) bool {
	// Only check manual mode
	if roomStatus.ThermSetpointMode != "manual" {
		return false
	}

	// No override if end time is not set
	if roomStatus.ThermSetpointEndTime == 0 {
		return false
	}

	// No start time available from API, can't calculate duration
	if roomStatus.ThermSetpointStartTime == 0 {
		// If in manual mode with end time but no start time, assume it was set by user
		return true
	}

	// Calculate duration in seconds
	durationSeconds := roomStatus.ThermSetpointEndTime - roomStatus.ThermSetpointStartTime

	// Human overrides are typically >= 60 minutes (Netatmo app defaults: 1h, 3h, 6h)
	// Algorithm-set overrides are < 15 minutes
	const sixtyMinutesInSeconds = 60 * 60
	return durationSeconds >= sixtyMinutesInSeconds
}
