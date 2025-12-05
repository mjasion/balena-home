package control

import (
	"time"

	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.uber.org/zap"
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

// shouldControlRoom determines if we should control this room based on its mode and state
// Returns true if we should proceed with control, false if we should skip
func (c *Controller) shouldControlRoom(roomStatus *netatmo.RoomStatus) (bool, string) {
	mode := roomStatus.ThermSetpointMode

	// Case 1: Room is in schedule mode - we can control
	if mode == "schedule" {
		return true, ""
	}

	// Case 2: Room is in manual mode
	if mode == "manual" {
		// Check if this is a human override (duration >= 60 min)
		if c.isHumanOverride(roomStatus) {
			return false, "human override detected (duration >= 60 min)"
		}

		// Manual mode but algorithm-set override - we can control
		// This includes overrides that expire within the current 15-minute window
		return true, ""
	}

	// Case 3: Home is in away or HG mode with no manual settings
	if mode == "away" || mode == "hg" {
		// Check if there's a manual override on top of away/hg
		if roomStatus.ThermSetpointEndTime > 0 {
			// Manual override on top of away/hg - skip
			return false, "manual override on top of " + mode + " mode"
		}

		// Pure away/hg mode - we can control
		return true, ""
	}

	// Unknown mode - skip for safety
	return false, "unknown thermostat mode: " + mode
}

// detectHomeModeChange detects if the home mode has changed (e.g., from normal to away/hg)
// Returns true if a mode change was detected that should reset the control state
func (c *Controller) detectHomeModeChange(roomStatus *netatmo.RoomStatus, roomID string) bool {
	c.stateMu.Lock()
	state, exists := c.stateByRoom[roomID]
	if !exists {
		// First time seeing this room, just store the mode
		c.stateMu.Unlock()
		return false
	}
	lastHomeMode := state.LastHomeMode
	c.stateMu.Unlock()

	currentMode := roomStatus.ThermSetpointMode

	// If this is the first time we're seeing this room, just store the mode
	if lastHomeMode == "" {
		c.stateMu.Lock()
		if state, exists := c.stateByRoom[roomID]; exists {
			state.LastHomeMode = currentMode
		}
		c.stateMu.Unlock()
		return false
	}

	// Check if mode changed from normal/manual to away/hg
	isNowSpecialMode := currentMode == "away" || currentMode == "hg"
	wasSpecialMode := lastHomeMode == "away" || lastHomeMode == "hg"

	// Detect transition TO away/hg mode
	if isNowSpecialMode && !wasSpecialMode {
		c.logger.Info("home mode changed to special mode",
			zap.String("room_id", roomID),
			zap.String("old_mode", lastHomeMode),
			zap.String("new_mode", currentMode),
		)

		c.stateMu.Lock()
		if state, exists := c.stateByRoom[roomID]; exists {
			state.LastHomeMode = currentMode
		}
		c.stateMu.Unlock()

		return true
	}

	// Detect transition FROM away/hg mode
	if !isNowSpecialMode && wasSpecialMode {
		c.logger.Info("home mode changed from special mode",
			zap.String("room_id", roomID),
			zap.String("old_mode", lastHomeMode),
			zap.String("new_mode", currentMode),
		)

		c.stateMu.Lock()
		if state, exists := c.stateByRoom[roomID]; exists {
			state.LastHomeMode = currentMode
		}
		c.stateMu.Unlock()

		return true
	}

	// Update last mode if changed but not to/from special modes
	if currentMode != lastHomeMode {
		c.stateMu.Lock()
		if state, exists := c.stateByRoom[roomID]; exists {
			state.LastHomeMode = currentMode
		}
		c.stateMu.Unlock()
	}

	return false
}

