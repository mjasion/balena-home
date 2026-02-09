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

	c.logger.Debug("evaluating room - starting decision process",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", mapping.RoomName),
	)

	decision := ControlDecision{
		RoomID:   mapping.RoomID,
		RoomName: mapping.RoomName,
		Action:   "skip",
	}

	// Get room status - required for all decisions
	roomStatus, roomExists := roomStatusMap[mapping.RoomID]
	if !roomExists {
		decision.Reason = "room not found in Netatmo status"
		c.logger.Warn("evaluating room - room not found in Netatmo home status",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", mapping.RoomName),
			zap.String("room_id", mapping.RoomID),
		)
		span.SetAttributes(attribute.String("skip_reason", "room_not_found"))
		return decision
	}

	if !roomStatus.Reachable {
		decision.Reason = "thermostat not reachable"
		c.logger.Info("evaluating room - thermostat not reachable, skipping",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", mapping.RoomName),
		)
		span.SetAttributes(attribute.String("skip_reason", "not_reachable"))
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
		c.logger.Warn("evaluating room - sensor data unavailable",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", mapping.RoomName),
			zap.String("sensor_mac", sensorMAC),
		)
		span.SetAttributes(attribute.String("skip_reason", "no_sensor_data"))
		return decision
	}
	decision.XiaomiTemperature = xiaomiTemp

	c.logger.Debug("evaluating room - sensor data retrieved",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", mapping.RoomName),
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("thermostat_measured", decision.ThermostatMeasured),
	)

	// Check if this is a human override (duration >= 60 minutes)
	if c.isHumanOverride(roomStatus) {
		decision.Reason = "human override detected (duration >= 60 min), skipping"
		c.logger.Info("evaluating room - human override detected, skipping control",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", mapping.RoomName),
			zap.String("mode", roomStatus.ThermSetpointMode),
		)
		span.SetAttributes(attribute.String("skip_reason", "human_override"))
		return decision
	}

	// Calculate temperature difference
	tempDiff := xiaomiTemp - decision.ScheduledTemp

	c.logger.Debug("evaluating room - temperature difference calculated",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", mapping.RoomName),
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("target_temp", decision.ScheduledTemp),
		zap.Float64("temp_diff", tempDiff),
		zap.Float64("threshold", c.config.TemperatureThreshold),
	)

	// Determine setpoint based on three-zone algorithm
	var calculatedSetpoint float64
	var zone string

	switch {
	case tempDiff <= -c.config.TemperatureThreshold:
		// Zone 1: Room too cold - add 0.5°C to trigger heating
		calculatedSetpoint = decision.ThermostatMeasured + 0.5
		zone = "too_cold"
		span.SetAttributes(attribute.String("zone", zone))
		c.logger.Info("evaluating room - three-zone algorithm: ZONE 1 (too cold)",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", mapping.RoomName),
			zap.Float64("temp_diff", tempDiff),
			zap.Float64("adjustment", 0.5),
			zap.String("action", "add 0.5°C to trigger heating"),
		)

	case tempDiff >= c.config.TemperatureThreshold:
		// Zone 3: Room too warm - subtract 0.5°C to stop heating
		calculatedSetpoint = decision.ThermostatMeasured - 0.5
		zone = "too_warm"
		span.SetAttributes(attribute.String("zone", zone))
		c.logger.Info("evaluating room - three-zone algorithm: ZONE 3 (too warm)",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", mapping.RoomName),
			zap.Float64("temp_diff", tempDiff),
			zap.Float64("adjustment", -0.5),
			zap.String("action", "subtract 0.5°C to stop heating"),
		)

	default:
		// Zone 2: Within range - maintain current reading
		calculatedSetpoint = decision.ThermostatMeasured
		zone = "within_range"
		span.SetAttributes(attribute.String("zone", zone))
		c.logger.Debug("evaluating room - three-zone algorithm: ZONE 2 (within range)",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", mapping.RoomName),
			zap.Float64("temp_diff", tempDiff),
			zap.String("action", "maintain current measured temperature"),
		)
	}

	// Round to 0.5°C increments (Netatmo requirement)
	calculatedSetpoint = roundToHalfDegree(calculatedSetpoint)

	// Apply safety bounds (7-30°C)
	calculatedSetpoint = c.applySafetyBounds(calculatedSetpoint)

	c.logger.Debug("evaluating room - setpoint calculated and bounded",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", mapping.RoomName),
		zap.Float64("raw_calculated", decision.ThermostatMeasured),
		zap.Float64("rounded", roundToHalfDegree(decision.ThermostatMeasured)),
		zap.Float64("final_setpoint", calculatedSetpoint),
	)

	// Check if setpoint already matches current (no change needed)
	if math.Abs(calculatedSetpoint-decision.SetpointTemperature) < 0.1 {
		decision.Action = "no_adjustment_needed"
		decision.Reason = fmt.Sprintf("setpoint already at target (%.1f°C)", calculatedSetpoint)

		c.logger.Info("evaluating room - no adjustment needed, setpoint already at target",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", mapping.RoomName),
			zap.Float64("current_setpoint", decision.SetpointTemperature),
			zap.Float64("target_setpoint", calculatedSetpoint),
		)

		span.SetAttributes(
			attribute.String("decision_action", "no_adjustment_needed"),
			attribute.Float64("current_setpoint", decision.SetpointTemperature),
			attribute.Float64("target_setpoint", calculatedSetpoint),
		)

		return decision
	}

	// Decision: Set manual override with configured duration
	decision.Action = "set_manual_override"
	decision.CalculatedSetpoint = calculatedSetpoint
	overrideEndTime := time.Now().Add(time.Duration(c.config.OverrideDurationMinutes) * time.Minute)
	decision.OverrideEndTime = overrideEndTime.Unix()

	reasonParts := []string{
		fmt.Sprintf("xiaomi=%.1f°C", xiaomiTemp),
		fmt.Sprintf("target=%.1f°C", decision.ScheduledTemp),
		fmt.Sprintf("measured=%.1f°C", decision.ThermostatMeasured),
		fmt.Sprintf("setpoint=%.1f°C", calculatedSetpoint),
	}

	hardOverrideActive := c.isHardOverrideActive(mapping.RoomName)
	if hardOverrideActive {
		reasonParts = append(reasonParts, "hard override")
	}

	decision.Reason = strings.Join(reasonParts, ", ")

	c.logger.Info("evaluating room - decision: set manual override",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", mapping.RoomName),
		zap.String("zone", zone),
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("target_temp", decision.ScheduledTemp),
		zap.Float64("temp_diff", tempDiff),
		zap.Float64("current_setpoint", decision.SetpointTemperature),
		zap.Float64("new_setpoint", calculatedSetpoint),
		zap.Time("override_end_time", overrideEndTime),
		zap.Bool("hard_override_active", hardOverrideActive),
	)

	span.SetAttributes(
		attribute.String("decision_action", decision.Action),
		attribute.String("decision_reason", decision.Reason),
		attribute.Float64("xiaomi_temperature", decision.XiaomiTemperature),
		attribute.Float64("scheduled_temperature", decision.ScheduledTemp),
		attribute.Float64("thermostat_measured", decision.ThermostatMeasured),
		attribute.Float64("setpoint_temperature", decision.SetpointTemperature),
		attribute.String("thermostat_mode", decision.ThermostatMode),
		attribute.Float64("calculated_setpoint", decision.CalculatedSetpoint),
		attribute.Bool("hard_override_active", hardOverrideActive),
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
