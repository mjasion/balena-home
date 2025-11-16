package control

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// evaluateAndExecuteRooms evaluates and executes control decisions for all rooms.
// Processes rooms sequentially to avoid API rate limiting.
// Returns counts of skipped, adjusted, and no-adjustment-needed rooms.
func (c *Controller) evaluateAndExecuteRooms(ctx context.Context, roomStatusMap map[string]*netatmo.RoomStatus) (skipCount, adjustCount, noAdjustCount int) {
	for _, mapping := range c.config.Mappings {
		decision := c.evaluateRoom(ctx, mapping, roomStatusMap)

		// Track decision types
		switch decision.Action {
		case "skip":
			skipCount++
		case "set_manual_override":
			adjustCount++
		case "no_adjustment_needed":
			noAdjustCount++
		}

		// Push metrics for this decision
		c.stateMu.RLock()
		state := c.stateByRoom[mapping.RoomID]
		externallyModified := false
		if state != nil {
			externallyModified = state.ExternallyModified
		}
		c.stateMu.RUnlock()

		// Check if hard override is active
		hardOverrideActive := c.isHardOverrideActive(mapping.RoomName)

		c.pushControlMetrics(decision, hardOverrideActive, externallyModified)
		c.executeDecision(ctx, decision)
	}

	return skipCount, adjustCount, noAdjustCount
}

// evaluateRoom evaluates whether a room needs thermostat adjustment.
// This is the main decision-making function that:
//  1. Retrieves room state and current status
//  2. Populates decision with temperature readings
//  3. Validates if the room can be controlled
//  4. Calculates optimal setpoint with sensor offset compensation
//
// The function respects user manual overrides and home mode changes (away, frost guard).
func (c *Controller) evaluateRoom(
	ctx context.Context,
	mapping config.ThermostatMapping,
	roomStatusMap map[string]*netatmo.RoomStatus,
) ControlDecision {
	ctx, span := c.tracer.Start(ctx, "evaluate_room_"+mapping.RoomName,
		trace.WithAttributes(
			attribute.String("room_name", mapping.RoomName),
			attribute.String("room_id", mapping.RoomID),
			attribute.String("sensor_mac", mapping.SensorMAC),
		),
	)

	decision := ControlDecision{
		RoomID:   mapping.RoomID,
		RoomName: mapping.RoomName,
		Action:   "skip",
	}

	defer func() {
		c.recordDecisionSpanAttributes(span, decision)
		span.End()
	}()

	// Get room state and status
	state, roomStatus := c.getRoomStateAndStatus(mapping.RoomID, roomStatusMap)
	if state == nil {
		decision.Reason = "room state not initialized"
		return decision
	}

	// Populate basic temperature fields
	c.populateDecisionTemperatures(ctx, &decision, mapping, roomStatus, state)

	// Validate if we can control this room
	if skipReason := c.validateRoomForControl(mapping, state, roomStatus); skipReason != "" {
		decision.Reason = skipReason
		return decision
	}

	// Calculate the setpoint decision
	return c.calculateSetpointDecision(ctx, mapping, state, roomStatus, decision)
}

// recordDecisionSpanAttributes records decision attributes on the span
func (c *Controller) recordDecisionSpanAttributes(span trace.Span, decision ControlDecision) {
	span.SetAttributes(
		attribute.String("decision_action", decision.Action),
		attribute.String("decision_reason", decision.Reason),
		attribute.Float64("xiaomi_temperature", decision.XiaomiTemperature),
		attribute.Float64("scheduled_temperature", decision.ScheduledTemp),
		attribute.Float64("thermostat_measured", decision.ThermostatMeasured),
		attribute.Float64("setpoint_temperature", decision.SetpointTemperature),
		attribute.String("thermostat_mode", decision.ThermostatMode),
	)
	if decision.Action == "set_manual_override" {
		span.SetAttributes(attribute.Float64("calculated_setpoint", decision.CalculatedSetpoint))
	}
}

// getRoomStateAndStatus gets the room state and status with a single lock
func (c *Controller) getRoomStateAndStatus(
	roomID string,
	roomStatusMap map[string]*netatmo.RoomStatus,
) (*ThermostatState, *netatmo.RoomStatus) {
	c.stateMu.RLock()
	state, exists := c.stateByRoom[roomID]
	c.stateMu.RUnlock()

	if !exists || state == nil {
		return nil, nil
	}

	roomStatus := roomStatusMap[roomID]
	return state, roomStatus
}

// populateDecisionTemperatures populates temperature fields in the decision
func (c *Controller) populateDecisionTemperatures(
	ctx context.Context,
	decision *ControlDecision,
	mapping config.ThermostatMapping,
	roomStatus *netatmo.RoomStatus,
	state *ThermostatState,
) {
	if roomStatus == nil || !roomStatus.Reachable {
		return
	}

	decision.ThermostatMeasured = roomStatus.ThermMeasuredTemperature
	decision.SetpointTemperature = roomStatus.ThermSetpointTemperature
	decision.ThermostatMode = roomStatus.ThermSetpointMode
	decision.ScheduledTemp = c.determineScheduledTemp(mapping.RoomName, state, roomStatus)

	// Get Xiaomi sensor readings
	sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
	if xiaomiTemp, err := c.getWeightedAverageTemperature(ctx, sensorMAC); err == nil {
		decision.XiaomiTemperature = xiaomiTemp
	}
}

// validateRoomForControl checks if the room can be controlled, returns skip reason if not
func (c *Controller) validateRoomForControl(
	mapping config.ThermostatMapping,
	state *ThermostatState,
	roomStatus *netatmo.RoomStatus,
) string {
	// Detect home mode changes and handle state resets
	if roomStatus != nil {
		c.detectHomeModeChange(state, roomStatus)
	}

	// Check for external manual changes
	if roomStatus != nil && c.detectExternalManualChange(state, roomStatus) {
		return "external manual change detected, respecting user intent"
	}

	// Check if we should control based on mode
	if roomStatus != nil {
		if shouldControl, skipReason := c.shouldControlRoom(state, roomStatus); !shouldControl {
			return skipReason
		}
	}

	// Handle legacy external modification flag
	if state.ExternallyModified {
		if roomStatus != nil && roomStatus.ThermSetpointMode == "schedule" {
			c.logger.Info("external modification cleared: thermostat returned to schedule mode",
				zap.String("room_name", mapping.RoomName),
			)
			c.clearExternalModification(mapping.RoomID)
			state.ExternallyModified = false
		} else {
			return "externally modified (legacy), respecting manual override"
		}
	}

	// Validate room exists and is reachable
	if roomStatus == nil {
		c.logger.Warn("room not found in Netatmo home status",
			zap.String("room_name", mapping.RoomName),
			zap.String("room_id", mapping.RoomID),
		)
		return "room not found in Netatmo status"
	}

	if !roomStatus.Reachable {
		return "thermostat not reachable"
	}

	return ""
}

// calculateSetpointDecision calculates the setpoint and builds the final decision
func (c *Controller) calculateSetpointDecision(
	ctx context.Context,
	mapping config.ThermostatMapping,
	state *ThermostatState,
	roomStatus *netatmo.RoomStatus,
	decision ControlDecision,
) ControlDecision {
	_ = ctx // context reserved for future use

	// Validate sensor data
	if decision.XiaomiTemperature == 0 {
		decision.Reason = "sensor data unavailable"
		c.logger.Warn("sensor data unavailable for control",
			zap.String("room_name", mapping.RoomName),
			zap.String("sensor_mac", strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))),
		)
		return decision
	}

	// Calculate setpoint with sensor offset compensation
	xiaomiTemp := decision.XiaomiTemperature
	scheduledTemp := decision.ScheduledTemp
	tempDiff := xiaomiTemp - scheduledTemp
	sensorOffset := decision.ThermostatMeasured - xiaomiTemp

	// Calculate compensated setpoint to account for sensor inaccuracy
	// If Netatmo reads 1°C too low, we need to set setpoint 1°C lower
	// so when Netatmo reaches that setpoint, actual temp will be at target
	rawSetpoint := scheduledTemp + sensorOffset
	calculatedSetpoint := roundToHalfDegree(rawSetpoint)

	c.logger.Debug("calculated compensated setpoint",
		zap.String("room_name", mapping.RoomName),
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("thermostat_measured", decision.ThermostatMeasured),
		zap.Float64("sensor_offset", sensorOffset),
		zap.Float64("scheduled_temp", scheduledTemp),
		zap.Float64("raw_setpoint", rawSetpoint),
		zap.Float64("calculated_setpoint", calculatedSetpoint),
	)

	// Check if override extension is needed
	shouldExtend, timeUntilExpiry := c.shouldExtendOverride(state)

	// Check if no adjustment is needed
	adjustmentDecision := c.checkIfAdjustmentNeeded(
		decision, calculatedSetpoint, scheduledTemp, tempDiff,
		roomStatus.ThermSetpointTemperature, shouldExtend, timeUntilExpiry,
	)
	if adjustmentDecision.Action != "" {
		return adjustmentDecision
	}

	// Apply safety bounds
	calculatedSetpoint = c.applySafetyBounds(calculatedSetpoint)

	// Build override decision
	decision.Action = "set_manual_override"
	decision.CalculatedSetpoint = calculatedSetpoint
	decision.OverrideEndTime = time.Now().Add(time.Duration(c.config.OverrideDurationMinutes) * time.Minute).Unix()
	decision.Reason = c.buildDecisionReason(
		xiaomiTemp, scheduledTemp, decision.ThermostatMeasured,
		sensorOffset, calculatedSetpoint, shouldExtend, timeUntilExpiry, mapping.RoomName,
	)

	// Final check for external modification (legacy detection)
	if c.detectExternalModification(state, roomStatus) {
		c.markExternallyModified(mapping.RoomID)
		decision.Action = "skip"
		decision.Reason = "external modification detected"
	}

	return decision
}

// checkIfAdjustmentNeeded checks if adjustment is needed or if current state is acceptable
func (c *Controller) checkIfAdjustmentNeeded(
	decision ControlDecision,
	calculatedSetpoint, scheduledTemp, tempDiff, currentSetpoint float64,
	shouldExtend bool,
	timeUntilExpiry time.Duration,
) ControlDecision {
	// Check if no sensor offset and temperature is fine
	noSensorOffset := math.Abs(calculatedSetpoint-scheduledTemp) < SetpointToleranceCelsius
	tempWithinThreshold := math.Abs(tempDiff) < c.config.TemperatureThreshold

	if noSensorOffset && tempWithinThreshold && !shouldExtend {
		decision.Action = "no_adjustment_needed"
		decision.Reason = fmt.Sprintf("no sensor offset detected and temperature within threshold (%.2f°C)", math.Abs(tempDiff))
		return decision
	}

	// Check if current setpoint already matches target
	if math.Abs(currentSetpoint-calculatedSetpoint) < SetpointToleranceCelsius && !shouldExtend {
		decision.Action = "no_adjustment_needed"
		decision.Reason = fmt.Sprintf("setpoint already at target (%.1f°C), mode=%s", calculatedSetpoint, decision.ThermostatMode)
		return decision
	}

	if shouldExtend {
		c.logger.Debug("extending override",
			zap.String("room_name", decision.RoomName),
			zap.Float64("calculated_setpoint", calculatedSetpoint),
			zap.Duration("time_until_expiry", timeUntilExpiry),
		)
	}

	// Return empty decision to indicate adjustment is needed
	decision.Action = ""
	return decision
}

// buildDecisionReason builds a human-readable reason for the decision
func (c *Controller) buildDecisionReason(
	xiaomiTemp, scheduledTemp, thermostatMeasured, sensorOffset, calculatedSetpoint float64,
	shouldExtend bool, timeUntilExpiry time.Duration, roomName string,
) string {
	reasonParts := []string{
		fmt.Sprintf("xiaomi=%.1f°C", xiaomiTemp),
		fmt.Sprintf("target=%.1f°C", scheduledTemp),
		fmt.Sprintf("netatmo=%.1f°C", thermostatMeasured),
		fmt.Sprintf("offset=%.2f°C", sensorOffset),
		fmt.Sprintf("setpoint=%.1f°C", calculatedSetpoint),
	}

	if c.isHardOverrideActive(roomName) {
		reasonParts = append(reasonParts, "hard override")
	} else if shouldExtend {
		reasonParts = append(reasonParts, fmt.Sprintf("extending, %.0fm left", timeUntilExpiry.Minutes()))
	}

	return strings.Join(reasonParts, ", ")
}

// determineScheduledTemp determines the scheduled temperature for a room.
// Priority order:
//  1. Hard overrides (from config)
//  2. Synced scheduled temperature (if recent)
//  3. Current setpoint temperature (fallback)
func (c *Controller) determineScheduledTemp(roomName string, state *ThermostatState, roomStatus *netatmo.RoomStatus) float64 {
	// Check for hard overrides first (highest precedence)
	for _, override := range c.config.HardOverrides {
		if override.RoomName == roomName {
			if temp, active := c.getHardOverrideTemp(override); active {
				return temp
			}
		}
	}

	// Prefer synced schedule temperature (if available and recent)
	if state.SyncedScheduledTemp > 0 && !state.SyncedScheduledTime.IsZero() {
		timeSinceSync := time.Since(state.SyncedScheduledTime)
		if timeSinceSync < MaxSyncedScheduleTempAge {
			c.logger.Debug("using synced schedule temperature",
				zap.String("room_name", roomName),
				zap.Float64("synced_temp", state.SyncedScheduledTemp),
				zap.Duration("time_since_sync", timeSinceSync),
			)
			return state.SyncedScheduledTemp
		}
		c.logger.Debug("synced schedule temperature too old, using current setpoint",
			zap.String("room_name", roomName),
			zap.Duration("time_since_sync", timeSinceSync),
		)
	}

	// Fallback: use current setpoint temperature
	return roomStatus.ThermSetpointTemperature
}


// roundToHalfDegree rounds a temperature to the nearest 0.5°C
// Netatmo thermostats only accept setpoints in 0.5°C increments
func roundToHalfDegree(temp float64) float64 {
	return math.Round(temp*2.0) / 2.0
}

// applySafetyBounds applies safety limits to the calculated setpoint
func (c *Controller) applySafetyBounds(setpoint float64) float64 {
	if setpoint < MinAbsoluteSetpointCelsius {
		return MinAbsoluteSetpointCelsius
	} else if setpoint > MaxAbsoluteSetpointCelsius {
		return MaxAbsoluteSetpointCelsius
	}
	return setpoint
}

// shouldExtendOverride checks if an existing override should be extended
func (c *Controller) shouldExtendOverride(state *ThermostatState) (bool, time.Duration) {
	if state.OverrideEndTime.IsZero() {
		return false, 0
	}

	timeUntilExpiry := time.Until(state.OverrideEndTime)
	extensionThreshold := time.Duration(c.config.ExtensionThresholdMinutes) * time.Minute

	if timeUntilExpiry > 0 && timeUntilExpiry < extensionThreshold {
		return true, timeUntilExpiry
	}

	return false, timeUntilExpiry
}

// detectExternalModification detects if the thermostat was manually changed
func (c *Controller) detectExternalModification(state *ThermostatState, roomStatus *netatmo.RoomStatus) bool {
	// Only check if we previously sent a command
	if state.LastSetpointTime.IsZero() {
		return false
	}

	timeSinceLastCommand := time.Since(state.LastSetpointTime)
	overrideExpired := !state.OverrideEndTime.IsZero() && time.Now().After(state.OverrideEndTime)

	// Only detect if: command was sent >2min ago, override not expired, not in schedule mode
	if timeSinceLastCommand <= MinTimeSinceCommandForDetection || overrideExpired || roomStatus.ThermSetpointMode == "schedule" {
		return false
	}

	// Check if current setpoint differs from what we sent
	delta := roomStatus.ThermSetpointTemperature - state.LastSetpoint
	if math.Abs(delta) > SetpointToleranceCelsius {
		c.logger.Warn("external modification detected - backing off from automation indefinitely",
			zap.String("room_name", state.RoomName),
			zap.Float64("expected_setpoint", state.LastSetpoint),
			zap.Float64("actual_setpoint", roomStatus.ThermSetpointTemperature),
			zap.Float64("delta", delta),
			zap.Duration("time_since_last_command", timeSinceLastCommand),
			zap.Time("last_command_sent_at", state.LastSetpointTime.In(c.warsawLocation())),
			zap.String("thermostat_mode", roomStatus.ThermSetpointMode),
			zap.String("resume_condition", "automation will resume only when thermostat is switched back to 'schedule' mode"),
		)
		return true
	}

	return false
}

// isHardOverrideActive checks if a hard override is currently active for a room
func (c *Controller) isHardOverrideActive(roomName string) bool {
	for _, override := range c.config.HardOverrides {
		if override.RoomName == roomName {
			_, active := c.getHardOverrideTemp(override)
			return active
		}
	}
	return false
}

// getHardOverrideTemp checks if a hard override is currently active and returns the target temperature
func (c *Controller) getHardOverrideTemp(override config.HardOverride) (float64, bool) {
	now := time.Now().In(c.warsawLocation())
	currentTime := now.Format("15:04")
	currentDay := now.Weekday().String()[:3] // "Mon", "Tue", etc.

	for _, window := range override.Schedule {
		// Check if time matches
		if currentTime >= window.StartTime && currentTime <= window.EndTime {
			// If days are specified, check if current day matches
			if len(window.Days) > 0 {
				dayMatches := false
				for _, day := range window.Days {
					normalizedDay := day
					if len(day) > 3 {
						normalizedDay = day[:3]
					}
					if normalizedDay == currentDay {
						dayMatches = true
						break
					}
				}
				if !dayMatches {
					continue
				}
			}
			return window.TargetTemperature, true
		}
	}
	return 0, false
}

