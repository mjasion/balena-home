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

// evaluateAndExecuteRooms evaluates and executes control decisions for all rooms
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
		hardOverrideActive := c.isHardOverrideActive(mapping.RoomName)
		c.pushControlMetrics(decision, hardOverrideActive, false)
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
	defer span.End()

	decision := ControlDecision{
		RoomID:   mapping.RoomID,
		RoomName: mapping.RoomName,
		Action:   "skip",
	}

	// Get room status - required for all decisions
	roomStatus, roomExists := roomStatusMap[mapping.RoomID]
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

	// Populate current status fields
	decision.ThermostatMeasured = roomStatus.ThermMeasuredTemperature
	decision.SetpointTemperature = roomStatus.ThermSetpointTemperature
	decision.ThermostatMode = roomStatus.ThermSetpointMode

	// Get scheduled temperature (respects hard overrides)
	decision.ScheduledTemp = c.determineScheduledTemp(mapping.RoomName, roomStatus)

	// Get Xiaomi sensor reading
	sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
	xiaomiTemp, err := c.getWeightedAverageTemperature(ctx, sensorMAC)
	if err != nil || xiaomiTemp == 0 {
		decision.Reason = "sensor data unavailable"
		c.logger.Warn("sensor data unavailable for control",
			zap.String("room_name", mapping.RoomName),
			zap.String("sensor_mac", sensorMAC),
		)
		return decision
	}
	decision.XiaomiTemperature = xiaomiTemp

	// Check if this is a human override (duration >= 60 minutes)
	if c.isHumanOverride(roomStatus) {
		decision.Reason = "human override detected (duration >= 60 min), skipping"
		return decision
	}

	// Calculate temperature difference
	tempDiff := xiaomiTemp - decision.ScheduledTemp

	// Determine setpoint based on three-zone algorithm
	var calculatedSetpoint float64

	switch {
	case tempDiff <= -c.config.TemperatureThreshold:
		// Zone 1: Room too cold - add 0.5°C to trigger heating
		calculatedSetpoint = decision.ThermostatMeasured + 0.5
		span.SetAttributes(attribute.String("zone", "too_cold"))

	case tempDiff >= c.config.TemperatureThreshold:
		// Zone 3: Room too warm - subtract 0.5°C to stop heating
		calculatedSetpoint = decision.ThermostatMeasured - 0.5
		span.SetAttributes(attribute.String("zone", "too_warm"))

	default:
		// Zone 2: Within range - maintain current reading
		calculatedSetpoint = decision.ThermostatMeasured
		span.SetAttributes(attribute.String("zone", "within_range"))
	}

	// Round to 0.5°C increments (Netatmo requirement)
	calculatedSetpoint = roundToHalfDegree(calculatedSetpoint)

	// Apply safety bounds (7-30°C)
	calculatedSetpoint = c.applySafetyBounds(calculatedSetpoint)

	c.logger.Debug("calculated setpoint",
		zap.String("room_name", mapping.RoomName),
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("scheduled_temp", decision.ScheduledTemp),
		zap.Float64("temp_diff", tempDiff),
		zap.Float64("thermostat_measured", decision.ThermostatMeasured),
		zap.Float64("calculated_setpoint", calculatedSetpoint),
		zap.Float64("threshold", c.config.TemperatureThreshold),
	)

	// Check if setpoint already matches current (no change needed)
	if math.Abs(calculatedSetpoint-decision.SetpointTemperature) < 0.1 {
		decision.Action = "no_adjustment_needed"
		decision.Reason = fmt.Sprintf("setpoint already at target (%.1f°C)", calculatedSetpoint)
		return decision
	}

	// Decision: Set manual override with boundary-aligned expiration
	decision.Action = "set_manual_override"
	decision.CalculatedSetpoint = calculatedSetpoint
	decision.OverrideEndTime = c.calculateBoundaryAlignedEndTime().Unix()

	reasonParts := []string{
		fmt.Sprintf("xiaomi=%.1f°C", xiaomiTemp),
		fmt.Sprintf("target=%.1f°C", decision.ScheduledTemp),
		fmt.Sprintf("measured=%.1f°C", decision.ThermostatMeasured),
		fmt.Sprintf("setpoint=%.1f°C", calculatedSetpoint),
	}

	if c.isHardOverrideActive(mapping.RoomName) {
		reasonParts = append(reasonParts, "hard override")
	}

	decision.Reason = strings.Join(reasonParts, ", ")

	span.SetAttributes(
		attribute.String("decision_action", decision.Action),
		attribute.String("decision_reason", decision.Reason),
		attribute.Float64("xiaomi_temperature", decision.XiaomiTemperature),
		attribute.Float64("scheduled_temperature", decision.ScheduledTemp),
		attribute.Float64("thermostat_measured", decision.ThermostatMeasured),
		attribute.Float64("setpoint_temperature", decision.SetpointTemperature),
		attribute.String("thermostat_mode", decision.ThermostatMode),
		attribute.Float64("calculated_setpoint", decision.CalculatedSetpoint),
	)

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

// roundToHalfDegree rounds a temperature to the nearest 0.5°C
// Netatmo thermostats only accept setpoints in 0.5°C increments
func roundToHalfDegree(temp float64) float64 {
	return math.Round(temp*2.0) / 2.0
}

// applySafetyBounds applies safety limits to the calculated setpoint
func (c *Controller) applySafetyBounds(setpoint float64) float64 {
	const minSetpoint = 7.0
	const maxSetpoint = 30.0

	if setpoint < minSetpoint {
		return minSetpoint
	} else if setpoint > maxSetpoint {
		return maxSetpoint
	}
	return setpoint
}

// calculateBoundaryAlignedEndTime returns the next 15-minute boundary minus 1 second
// For example: 12:00:03 → 12:14:59, 12:15:30 → 12:29:59
func (c *Controller) calculateBoundaryAlignedEndTime() time.Time {
	now := time.Now()
	minute := now.Minute()

	// Determine which boundary this falls into
	var nextBoundary int
	if minute < 15 {
		nextBoundary = 15
	} else if minute < 30 {
		nextBoundary = 30
	} else if minute < 45 {
		nextBoundary = 45
	} else {
		nextBoundary = 0 // Next hour
	}

	// Calculate end time: next boundary minus 1 second (e.g., 14:59:59)
	if nextBoundary == 0 {
		// Next hour
		return now.Add(time.Hour).Truncate(time.Hour).Add(-1 * time.Second)
	}

	// Same hour
	endMinute := nextBoundary - 1
	endSecond := 59
	endTime := now.Truncate(time.Hour).Add(time.Duration(nextBoundary) * time.Minute).Add(-1 * time.Second)
	return endTime
}
