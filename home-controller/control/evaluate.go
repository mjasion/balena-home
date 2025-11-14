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

// evaluateAndExecuteRooms evaluates and executes control decisions for all rooms (sequential loop)
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

// evaluateRoom evaluates whether a room needs thermostat adjustment
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
		// Record decision attributes before span ends
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
		span.End()
	}()

	// Get current state (defensive copy)
	c.stateMu.RLock()
	state, exists := c.stateByRoom[mapping.RoomID]
	if !exists {
		c.stateMu.RUnlock()
		decision.Reason = "room state not initialized"
		return decision
	}
	stateCopy := state.Copy()
	c.stateMu.RUnlock()

	// Get room status early to populate temperature fields in all cases
	roomStatus, roomExists := roomStatusMap[mapping.RoomID]
	if roomExists && roomStatus.Reachable {
		decision.ThermostatMeasured = roomStatus.ThermMeasuredTemperature
		decision.SetpointTemperature = roomStatus.ThermSetpointTemperature
		decision.ThermostatMode = roomStatus.ThermSetpointMode

		// Determine scheduled temperature
		decision.ScheduledTemp = c.determineScheduledTemp(mapping.RoomName, &stateCopy, roomStatus)

		// Get Xiaomi sensor readings (last 60 seconds, weighted average)
		sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
		xiaomiTemp, err := c.getWeightedAverageTemperature(sensorMAC)
		if err == nil {
			decision.XiaomiTemperature = xiaomiTemp
		}
	}

	// Check precedence hierarchy: external modification detected - skip control entirely
	if stateCopy.ExternallyModified {
		if roomExists && roomStatus.ThermSetpointMode == "schedule" {
			c.logger.Info("external modification cleared: thermostat returned to schedule mode",
				zap.String("room_name", mapping.RoomName),
			)
			c.clearExternalModification(mapping.RoomID)
		} else {
			decision.Reason = "externally modified, respecting manual override"
			return decision
		}
	}

	// Check if we should extend an existing override
	shouldExtend, timeUntilExpiry := c.shouldExtendOverride(&stateCopy)

	// Validate room status is available and reachable
	if !roomExists {
		decision.Reason = "room not found in Netatmo status"
		c.logger.Warn("room not found in Netatmo home status",
			zap.String("room_name", mapping.RoomName),
			zap.String("room_id", mapping.RoomID),
		)
		return decision
	}

	if !roomStatus.Reachable {
		decision.Reason = "thermostat not reachable"
		return decision
	}

	// Validate Xiaomi sensor data is available
	if decision.XiaomiTemperature == 0 {
		decision.Reason = "sensor data unavailable"
		c.logger.Warn("sensor data unavailable for control",
			zap.String("room_name", mapping.RoomName),
			zap.String("sensor_mac", strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))),
		)
		return decision
	}

	// Calculate temperature difference
	xiaomiTemp := decision.XiaomiTemperature
	scheduledTemp := decision.ScheduledTemp
	tempDiff := xiaomiTemp - scheduledTemp

	// Check if temperature is within acceptable range
	withinThreshold := math.Abs(tempDiff) < c.config.TemperatureThreshold

	// If within threshold and no extension needed, skip action
	if withinThreshold && !shouldExtend {
		decision.Action = "no_adjustment_needed"
		decision.Reason = fmt.Sprintf("temperature difference %.2f°C below threshold %.2f°C",
			math.Abs(tempDiff), c.config.TemperatureThreshold)
		return decision
	}

	// Calculate setpoint using three-zone control strategy
	calculatedSetpoint := c.calculateSetpoint(tempDiff, scheduledTemp, decision.ThermostatMeasured)

	// Check if setpoint matches schedule (within 0.1°C tolerance)
	if math.Abs(calculatedSetpoint-scheduledTemp) < 0.1 {
		if !shouldExtend {
			decision.Action = "no_adjustment_needed"
			decision.Reason = fmt.Sprintf("calculated setpoint (%.1f°C) matches schedule, no override needed", calculatedSetpoint)
			return decision
		}
		c.logger.Debug("extending override even though setpoint matches schedule",
			zap.String("room_name", mapping.RoomName),
			zap.Float64("calculated_setpoint", calculatedSetpoint),
			zap.Duration("time_until_expiry", timeUntilExpiry),
		)
	}

	// Apply safety bounds
	calculatedSetpoint = c.applySafetyBounds(calculatedSetpoint)

	decision.Action = "set_manual_override"
	decision.CalculatedSetpoint = calculatedSetpoint
	decision.OverrideEndTime = time.Now().Add(time.Duration(c.config.OverrideDurationMinutes) * time.Minute).Unix()

	// Build reason message
	reasonSuffix := ""
	if c.isHardOverrideActive(mapping.RoomName) {
		reasonSuffix = " (hard override)"
	} else if shouldExtend {
		reasonSuffix = fmt.Sprintf(" (extending, %.0fm left)", timeUntilExpiry.Minutes())
	}
	decision.Reason = fmt.Sprintf("xiaomi=%.1f°C, target=%.1f°C, diff=%.2f°C%s",
		xiaomiTemp, scheduledTemp, tempDiff, reasonSuffix)

	// Detect external modification
	if c.detectExternalModification(&stateCopy, roomStatus) {
		c.markExternallyModified(mapping.RoomID)
		decision.Action = "skip"
		decision.Reason = "external modification detected"
	}

	return decision
}

// determineScheduledTemp determines the scheduled temperature for a room
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
		if timeSinceSync < 1*time.Hour {
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

// calculateSetpoint calculates the target setpoint using three-zone control
func (c *Controller) calculateSetpoint(tempDiff, scheduledTemp, thermostatMeasured float64) float64 {
	var setpoint float64

	if tempDiff < -c.config.TemperatureThreshold {
		// Room too cold - need to heat
		setpoint = math.Max(scheduledTemp, thermostatMeasured+0.5)
	} else if tempDiff > c.config.TemperatureThreshold {
		// Room too warm - need to cool (or stop heating)
		setpoint = math.Min(scheduledTemp, thermostatMeasured-0.5)
	} else {
		// Temperature within acceptable range but extension needed
		setpoint = thermostatMeasured
	}

	return setpoint
}

// applySafetyBounds applies safety limits to the calculated setpoint
func (c *Controller) applySafetyBounds(setpoint float64) float64 {
	if setpoint < 7.0 {
		return 7.0
	} else if setpoint > 30.0 {
		return 30.0
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
	if timeSinceLastCommand <= 2*time.Minute || overrideExpired || roomStatus.ThermSetpointMode == "schedule" {
		return false
	}

	// Check if current setpoint differs from what we sent
	delta := roomStatus.ThermSetpointTemperature - state.LastSetpoint
	if math.Abs(delta) > 0.1 {
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
