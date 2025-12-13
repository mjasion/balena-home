package control

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// evaluateRoom evaluates whether a room needs thermostat adjustment
func (c *Controller) evaluateRoom(
	ctx context.Context,
	mapping config.ThermostatMapping,
	roomStatusMap map[string]*netatmo.RoomStatus,
) ControlDecision {
	ctx, span := c.startRoomSpan(ctx, "evaluate_room", mapping.RoomName, mapping.RoomID)
	defer span.End()

	span.SetAttributes(attribute.String("sensor_mac", mapping.SensorMAC))

	c.logEvaluationDebug(span, mapping.RoomName, "evaluating room - starting decision process")

	decision := c.initDecision(mapping)

	// Validate room status
	roomStatus, ok := c.validateRoomStatus(ctx, span, mapping, roomStatusMap, &decision)
	if !ok {
		return decision
	}

	// Populate current status
	c.populateCurrentStatus(&decision, roomStatus)

	// Get sensor reading
	xiaomiTemp, ok := c.getSensorReading(ctx, span, mapping, &decision)
	if !ok {
		return decision
	}

	// Check for human override
	if c.shouldSkipDueToOverride(span, mapping.RoomName, roomStatus, &decision) {
		return decision
	}

	// Calculate control action
	return c.calculateControlAction(ctx, span, mapping, roomStatus, xiaomiTemp, decision)
}

// initDecision creates an initial decision with default skip action
func (c *Controller) initDecision(mapping config.ThermostatMapping) ControlDecision {
	return ControlDecision{
		RoomID:   mapping.RoomID,
		RoomName: mapping.RoomName,
		Action:   "skip",
	}
}

// validateRoomStatus checks if room exists and is reachable
func (c *Controller) validateRoomStatus(ctx context.Context, span trace.Span, mapping config.ThermostatMapping,
	roomStatusMap map[string]*netatmo.RoomStatus, decision *ControlDecision) (*netatmo.RoomStatus, bool) {

	roomStatus, roomExists := roomStatusMap[mapping.RoomID]
	if !roomExists {
		decision.Reason = "room not found in Netatmo status"
		c.logEvaluationWarn(span, mapping.RoomName,
			"evaluating room - room not found in Netatmo home status",
			zap.String("room_id", mapping.RoomID))
		recordSkipReason(span, "room_not_found")
		return nil, false
	}

	if !roomStatus.Reachable {
		decision.Reason = "thermostat not reachable"
		c.logEvaluationInfo(span, mapping.RoomName,
			"evaluating room - thermostat not reachable, skipping")
		recordSkipReason(span, "not_reachable")
		return nil, false
	}

	return roomStatus, true
}

// logEvaluationInfo is a helper for info-level evaluation logging
func (c *Controller) logEvaluationInfo(span trace.Span, roomName, message string, fields ...zap.Field) {
	allFields := []zap.Field{
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
	}
	allFields = append(allFields, fields...)
	c.logger.Info(message, allFields...)
}

// populateCurrentStatus fills in current thermostat status fields
func (c *Controller) populateCurrentStatus(decision *ControlDecision, roomStatus *netatmo.RoomStatus) {
	decision.ThermostatMeasured = roomStatus.ThermMeasuredTemperature
	decision.SetpointTemperature = roomStatus.ThermSetpointTemperature
	decision.ThermostatMode = roomStatus.ThermSetpointMode
	decision.ScheduledTemp = c.determineScheduledTemp(decision.RoomName, roomStatus)
}

// getSensorReading retrieves and validates Xiaomi sensor reading
func (c *Controller) getSensorReading(ctx context.Context, span trace.Span, mapping config.ThermostatMapping, decision *ControlDecision) (float64, bool) {
	sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
	xiaomiTemp, err := c.getWeightedAverageTemperature(ctx, sensorMAC)

	if err != nil || xiaomiTemp == 0 {
		decision.Reason = "sensor data unavailable"
		c.logEvaluationWarn(span, mapping.RoomName,
			"evaluating room - sensor data unavailable",
			zap.String("sensor_mac", sensorMAC))
		recordSkipReason(span, "no_sensor_data")
		return 0, false
	}

	decision.XiaomiTemperature = xiaomiTemp

	c.logEvaluationDebug(span, mapping.RoomName,
		"evaluating room - sensor data retrieved",
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("thermostat_measured", decision.ThermostatMeasured))

	return xiaomiTemp, true
}

// shouldSkipDueToOverride checks if we should skip due to human override
func (c *Controller) shouldSkipDueToOverride(span trace.Span, roomName string, roomStatus *netatmo.RoomStatus, decision *ControlDecision) bool {
	if !c.isHumanOverride(roomStatus) {
		return false
	}

	decision.Reason = "human override detected (duration >= 60 min), skipping"
	c.logEvaluationInfo(span, roomName,
		"evaluating room - human override detected, skipping control",
		zap.String("mode", roomStatus.ThermSetpointMode))
	recordSkipReason(span, "human_override")
	return true
}

// calculateControlAction implements the three-zone algorithm and determines action
func (c *Controller) calculateControlAction(ctx context.Context, span trace.Span, mapping config.ThermostatMapping,
	roomStatus *netatmo.RoomStatus, xiaomiTemp float64, decision ControlDecision) ControlDecision {

	tempDiff := xiaomiTemp - decision.ScheduledTemp

	c.logEvaluationDebug(span, mapping.RoomName,
		"evaluating room - temperature difference calculated",
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("target_temp", decision.ScheduledTemp),
		zap.Float64("temp_diff", tempDiff),
		zap.Float64("threshold", c.config.TemperatureThreshold))

	// Apply three-zone algorithm
	result := calculateThreeZoneSetpoint(xiaomiTemp, decision.ScheduledTemp,
		decision.ThermostatMeasured, c.config.TemperatureThreshold)

	span.SetAttributes(attribute.String("zone", result.Zone))

	c.logZoneDecision(span, mapping.RoomName, result, tempDiff)

	// Round and apply safety bounds
	calculatedSetpoint := applySafetyBounds(roundToHalfDegree(result.CalculatedSetpoint))

	c.logEvaluationDebug(span, mapping.RoomName,
		"evaluating room - setpoint calculated and bounded",
		zap.Float64("raw_calculated", result.CalculatedSetpoint),
		zap.Float64("rounded", roundToHalfDegree(result.CalculatedSetpoint)),
		zap.Float64("final_setpoint", calculatedSetpoint))

	// Check if adjustment is needed
	if !needsAdjustment(decision.SetpointTemperature, calculatedSetpoint) {
		return c.buildNoAdjustmentDecision(span, mapping.RoomName, decision, calculatedSetpoint)
	}

	// Build override decision
	return c.buildOverrideDecision(span, mapping.RoomName, decision, xiaomiTemp, calculatedSetpoint, result.Zone, tempDiff)
}

// logZoneDecision logs the three-zone algorithm decision
func (c *Controller) logZoneDecision(span trace.Span, roomName string, result AlgorithmResult, tempDiff float64) {
	switch result.Zone {
	case "too_cold":
		c.logEvaluation(span, roomName, "evaluating room - three-zone algorithm: ZONE 1 (too cold)",
			zap.Float64("temp_diff", tempDiff),
			zap.Float64("adjustment", result.Adjustment),
			zap.String("action", "add 0.5°C to trigger heating"))

	case "too_warm":
		c.logEvaluation(span, roomName, "evaluating room - three-zone algorithm: ZONE 3 (too warm)",
			zap.Float64("temp_diff", tempDiff),
			zap.Float64("adjustment", result.Adjustment),
			zap.String("action", "subtract 0.5°C to stop heating"))

	default: // within_range
		c.logEvaluationDebug(span, roomName, "evaluating room - three-zone algorithm: ZONE 2 (within range)",
			zap.Float64("temp_diff", tempDiff),
			zap.String("action", "maintain current measured temperature"))
	}
}

// buildNoAdjustmentDecision builds a decision when no adjustment is needed
func (c *Controller) buildNoAdjustmentDecision(span trace.Span, roomName string, decision ControlDecision, calculatedSetpoint float64) ControlDecision {
	decision.Action = "no_adjustment_needed"
	decision.Reason = fmt.Sprintf("setpoint already at target (%.1f°C)", calculatedSetpoint)

	c.logEvaluation(span, roomName,
		"evaluating room - no adjustment needed, setpoint already at target",
		zap.Float64("current_setpoint", decision.SetpointTemperature),
		zap.Float64("target_setpoint", calculatedSetpoint))

	span.SetAttributes(
		attribute.String("decision_action", "no_adjustment_needed"),
		attribute.Float64("current_setpoint", decision.SetpointTemperature),
		attribute.Float64("target_setpoint", calculatedSetpoint),
	)

	return decision
}

// buildOverrideDecision builds a manual override decision
func (c *Controller) buildOverrideDecision(span trace.Span, roomName string, decision ControlDecision,
	xiaomiTemp, calculatedSetpoint float64, zone string, tempDiff float64) ControlDecision {

	decision.Action = "set_manual_override"
	decision.CalculatedSetpoint = calculatedSetpoint
	overrideEndTime := calculateBoundaryAlignedEndTime()
	decision.OverrideEndTime = overrideEndTime.Unix()

	hardOverrideActive := c.isHardOverrideActive(roomName)
	decision.Reason = buildDecisionReason(xiaomiTemp, decision.ScheduledTemp,
		decision.ThermostatMeasured, calculatedSetpoint, hardOverrideActive)

	c.logEvaluation(span, roomName,
		"evaluating room - decision: set manual override",
		zap.String("zone", zone),
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("target_temp", decision.ScheduledTemp),
		zap.Float64("temp_diff", tempDiff),
		zap.Float64("current_setpoint", decision.SetpointTemperature),
		zap.Float64("new_setpoint", calculatedSetpoint),
		zap.Time("override_end_time", overrideEndTime),
		zap.Bool("hard_override_active", hardOverrideActive))

	recordDecisionAttributes(span, decision, hardOverrideActive)

	return decision
}

// determineScheduledTemp determines the scheduled temperature for a room
func (c *Controller) determineScheduledTemp(roomName string, roomStatus *netatmo.RoomStatus) float64 {
	// Check for hard overrides first (highest precedence)
	for _, override := range c.config.HardOverrides {
		if override.RoomName == roomName {
			if temp, active := c.getHardOverrideTemp(override); active {
				return temp
			}
		}
	}

	// Use current setpoint (could be schedule or manual override)
	// If thermostat is in schedule mode, this is the scheduled temp
	// If in manual mode, we use this as the target for compensation
	return roomStatus.ThermSetpointTemperature
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

// warsawLocation returns the Warsaw timezone (Poland, where the system is deployed)
func (c *Controller) warsawLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		// Fallback to UTC if timezone not available
		return time.UTC
	}
	return loc
}
