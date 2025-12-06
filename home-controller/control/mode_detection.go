package control

import (
	"math"
	"time"

	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.uber.org/zap"
)

// detectHomeModeChange detects if the home mode has changed (e.g., from normal to away/hg)
// Returns true if a mode change was detected that should reset the control state
func (c *Controller) detectHomeModeChange(state *ThermostatState, roomStatus *netatmo.RoomStatus) bool {
	currentMode := roomStatus.ThermSetpointMode

	// If this is the first time we're seeing this room, just store the mode
	if state.LastHomeMode == "" {
		c.stateMu.Lock()
		c.stateByRoom[state.RoomID].LastHomeMode = currentMode
		c.stateMu.Unlock()
		return false
	}

	// Check if mode changed from normal/manual to away/hg
	isNowSpecialMode := currentMode == "away" || currentMode == "hg"
	wasSpecialMode := state.LastHomeMode == "away" || state.LastHomeMode == "hg"

	// Detect transition TO away/hg mode (should reset external modification flag)
	if isNowSpecialMode && !wasSpecialMode {
		c.logger.Info("home mode changed to special mode, resetting control state",
			zap.String("room_name", state.RoomName),
			zap.String("old_mode", state.LastHomeMode),
			zap.String("new_mode", currentMode),
		)

		// Update last mode
		c.stateMu.Lock()
		c.stateByRoom[state.RoomID].LastHomeMode = currentMode
		// Clear external modification flag - start fresh
		c.stateByRoom[state.RoomID].ExternallyModified = false
		c.stateByRoom[state.RoomID].ExternalModificationTime = time.Time{}
		c.stateMu.Unlock()

		return true
	}

	// Detect transition FROM away/hg mode (should reset external modification flag)
	if !isNowSpecialMode && wasSpecialMode {
		c.logger.Info("home mode changed from special mode, resetting control state",
			zap.String("room_name", state.RoomName),
			zap.String("old_mode", state.LastHomeMode),
			zap.String("new_mode", currentMode),
		)

		// Update last mode
		c.stateMu.Lock()
		c.stateByRoom[state.RoomID].LastHomeMode = currentMode
		// Clear external modification flag - start fresh
		c.stateByRoom[state.RoomID].ExternallyModified = false
		c.stateByRoom[state.RoomID].ExternalModificationTime = time.Time{}
		c.stateMu.Unlock()

		return true
	}

	// Update last mode if changed but not to/from special modes
	if currentMode != state.LastHomeMode {
		c.stateMu.Lock()
		c.stateByRoom[state.RoomID].LastHomeMode = currentMode
		c.stateMu.Unlock()
	}

	return false
}

// detectExternalManualChange detects if the manual setpoint or endtime was changed externally
// (i.e., by the user directly changing the thermostat or via the app)
// Returns true if we should respect the external change and skip automation
func (c *Controller) detectExternalManualChange(state *ThermostatState, roomStatus *netatmo.RoomStatus) bool {
	// Only check if thermostat is in manual mode
	if roomStatus.ThermSetpointMode != "manual" {
		return false
	}

	// Skip if we haven't sent any commands yet
	if state.LastSetpointTime.IsZero() {
		// Store the current manual setpoint as baseline
		c.stateMu.Lock()
		c.stateByRoom[state.RoomID].LastManualSetpoint = roomStatus.ThermSetpointTemperature
		c.stateByRoom[state.RoomID].LastManualEndTime = roomStatus.ThermSetpointEndTime
		c.stateMu.Unlock()
		return false
	}

	// Wait at least 2 minutes since our last command before detecting external changes
	// (to allow API propagation time)
	timeSinceLastCommand := time.Since(state.LastSetpointTime)
	if timeSinceLastCommand < 2*time.Minute {
		return false
	}

	// Check if setpoint changed from what we last observed
	setpointDelta := math.Abs(roomStatus.ThermSetpointTemperature - state.LastManualSetpoint)
	endTimeChanged := roomStatus.ThermSetpointEndTime != state.LastManualEndTime

	if setpointDelta > 0.1 || endTimeChanged {
		c.logger.Info("external manual change detected, respecting user's intent",
			zap.String("room_name", state.RoomName),
			zap.Float64("old_setpoint", state.LastManualSetpoint),
			zap.Float64("new_setpoint", roomStatus.ThermSetpointTemperature),
			zap.Int64("old_endtime", state.LastManualEndTime),
			zap.Int64("new_endtime", roomStatus.ThermSetpointEndTime),
			zap.Duration("time_since_last_command", timeSinceLastCommand),
		)

		// Update tracked values
		c.stateMu.Lock()
		c.stateByRoom[state.RoomID].LastManualSetpoint = roomStatus.ThermSetpointTemperature
		c.stateByRoom[state.RoomID].LastManualEndTime = roomStatus.ThermSetpointEndTime
		// Mark as externally modified
		c.stateByRoom[state.RoomID].ExternallyModified = true
		c.stateByRoom[state.RoomID].ExternalModificationTime = time.Now()
		c.stateMu.Unlock()

		return true
	}

	// Update tracked values even if no change (keep them in sync)
	if state.LastManualSetpoint != roomStatus.ThermSetpointTemperature ||
		state.LastManualEndTime != roomStatus.ThermSetpointEndTime {
		c.stateMu.Lock()
		c.stateByRoom[state.RoomID].LastManualSetpoint = roomStatus.ThermSetpointTemperature
		c.stateByRoom[state.RoomID].LastManualEndTime = roomStatus.ThermSetpointEndTime
		c.stateMu.Unlock()
	}

	return false
}

// shouldControlRoom determines if we should control this room based on its mode and state
// Returns true if we should proceed with control, false if we should skip
func (c *Controller) shouldControlRoom(state *ThermostatState, roomStatus *netatmo.RoomStatus) (bool, string) {
	mode := roomStatus.ThermSetpointMode

	// Case 1: Room is in schedule mode - we can control
	if mode == "schedule" {
		return true, ""
	}

	// Case 2: Room is in manual mode and was set by our algorithm - we can control/extend
	if mode == "manual" {
		// Check if this is our override (we set it recently)
		if !state.LastSetpointTime.IsZero() && time.Since(state.LastSetpointTime) < 15*time.Minute {
			// Check if setpoint matches what we set (within tolerance)
			setpointDelta := math.Abs(roomStatus.ThermSetpointTemperature - state.LastSetpoint)
			if setpointDelta < 0.3 {
				// This is our override - we can extend it
				return true, ""
			}
		}

		// Manual mode but not our override - check if externally modified
		if state.ExternallyModified {
			return false, "externally modified manual override, respecting user intent"
		}

		// Manual mode, unknown origin - treat conservatively
		return false, "manual mode not set by algorithm"
	}

	// Case 3: Home is in away or HG mode with no manual settings - we can control
	if mode == "away" || mode == "hg" {
		// Check if there's a manual override on top of away/hg
		// If ThermSetpointEndTime is set, it means there's a temporary manual override
		if roomStatus.ThermSetpointEndTime > 0 {
			// Manual override on top of away/hg - skip
			return false, "manual override on top of " + mode + " mode"
		}

		// Pure away/hg mode - we can control to adjust for sensor differences
		return true, ""
	}

	// Unknown mode - skip for safety
	return false, "unknown thermostat mode: " + mode
}
