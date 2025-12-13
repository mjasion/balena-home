package control

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// executeDecision executes the control decision
func (c *Controller) executeDecision(ctx context.Context, decision ControlDecision) {
	ctx, span := c.startRoomSpan(ctx, "execute_decision", decision.RoomName, decision.RoomID)
	defer span.End()

	span.SetAttributes(attribute.String("action", decision.Action))

	c.logExecutionDebug(span, decision.RoomName, "executing decision",
		zap.String("action", decision.Action),
		zap.String("reason", decision.Reason))

	if decision.Action == "skip" || decision.Action == "no_adjustment_needed" {
		c.logDecision(span, decision)
		return
	}

	if decision.Action == "set_manual_override" {
		c.executeManualOverride(ctx, span, decision)
	}
}

// logDecision logs the control decision
func (c *Controller) logDecision(span trace.Span, decision ControlDecision) {
	logFields := []zap.Field{
		zap.String("action", decision.Action),
		zap.String("reason", decision.Reason),
		zap.Float64("xiaomi_temp", decision.XiaomiTemperature),
		zap.Float64("scheduled_temp", decision.ScheduledTemp),
		zap.Float64("setpoint_temp", decision.SetpointTemperature),
		zap.Float64("thermostat_measured", decision.ThermostatMeasured),
		zap.String("thermostat_mode", decision.ThermostatMode),
	}

	if c.config.DryRun {
		c.logExecutionInfo(span, decision.RoomName, "[DRY-RUN] would NOT set temperature", logFields...)
	} else {
		c.logExecutionInfo(span, decision.RoomName, "control decision", logFields...)
	}
}

// executeManualOverride executes a manual override decision
func (c *Controller) executeManualOverride(ctx context.Context, span trace.Span, decision ControlDecision) {
	c.logExecutionDebug(span, decision.RoomName, "executing manual override - applying safety limits",
		zap.Float64("calculated_setpoint", decision.CalculatedSetpoint))

	// Apply safety limits
	safeSetpoint, clamped := c.applyConfigSafetyLimits(decision.CalculatedSetpoint, decision.RoomName)
	overrideEndTime := time.Unix(decision.OverrideEndTime, 0)
	overrideDuration := calculateOverrideDuration(decision.OverrideEndTime)

	recordExecutionAttributes(span, safeSetpoint, clamped, overrideDuration, c.config.DryRun, false)

	if clamped {
		c.logExecutionWarn(span, decision.RoomName, "executing manual override - setpoint clamped by safety limits",
			zap.Float64("original", decision.CalculatedSetpoint),
			zap.Float64("clamped_to", safeSetpoint))
	}

	// Dry-run mode: log what would be sent but don't call API
	if c.config.DryRun {
		c.logDryRunOverride(span, decision, safeSetpoint, clamped, overrideEndTime, overrideDuration)
		c.updateRoomState(decision.RoomID, safeSetpoint, overrideEndTime)
		return
	}

	// Execute actual override
	c.executeActualOverride(ctx, span, decision, safeSetpoint, clamped, overrideEndTime, overrideDuration)
}

// logDryRunOverride logs the dry-run override information
func (c *Controller) logDryRunOverride(span trace.Span, decision ControlDecision, safeSetpoint float64,
	clamped bool, overrideEndTime time.Time, overrideDuration time.Duration) {

	c.logExecutionInfo(span, decision.RoomName, "[DRY-RUN] executing manual override - WOULD set temperature (not sent to API)",
		zap.String("room_id", decision.RoomID),
		zap.Float64("new_setpoint", safeSetpoint),
		zap.Float64("original_calculated_setpoint", decision.CalculatedSetpoint),
		zap.Bool("clamped", clamped),
		zap.Float64("xiaomi_temp", decision.XiaomiTemperature),
		zap.Float64("scheduled_temp", decision.ScheduledTemp),
		zap.Float64("thermostat_measured", decision.ThermostatMeasured),
		zap.String("reason", decision.Reason),
		zap.Float64("setpoint_temp", decision.SetpointTemperature),
		zap.String("thermostat_mode", decision.ThermostatMode),
		zap.Time("override_end_time", overrideEndTime.In(c.warsawLocation())),
		zap.Duration("override_duration", overrideDuration))

	span.SetAttributes(
		attribute.Bool("dry_run", true),
		attribute.Bool("api_call_made", false),
	)
}

// executeActualOverride executes the actual override via Netatmo API
func (c *Controller) executeActualOverride(ctx context.Context, span trace.Span, decision ControlDecision,
	safeSetpoint float64, clamped bool, overrideEndTime time.Time, overrideDuration time.Duration) {

	c.logExecutionInfo(span, decision.RoomName, "executing manual override - preparing to call Netatmo API",
		zap.Float64("new_setpoint", safeSetpoint),
		zap.Float64("original_calculated_setpoint", decision.CalculatedSetpoint),
		zap.Bool("clamped", clamped),
		zap.Float64("xiaomi_temp", decision.XiaomiTemperature),
		zap.Float64("scheduled_temp", decision.ScheduledTemp),
		zap.Float64("thermostat_measured", decision.ThermostatMeasured),
		zap.String("reason", decision.Reason),
		zap.Float64("current_setpoint", decision.SetpointTemperature),
		zap.String("thermostat_mode", decision.ThermostatMode),
		zap.Time("override_end_time", overrideEndTime.In(c.warsawLocation())),
		zap.Duration("override_duration", overrideDuration))

	// Call Netatmo API
	err := c.setNetatmoThermostat(ctx, decision.RoomID, decision.RoomName, safeSetpoint, decision.OverrideEndTime)
	if err != nil {
		recordError(span, err, "failed to set thermostat setpoint")
		span.SetAttributes(attribute.Bool("api_call_success", false))

		c.logExecutionError(span, decision.RoomName, "executing manual override - Netatmo API call failed", err)
		return
	}

	span.SetAttributes(
		attribute.Bool("api_call_success", true),
		attribute.Bool("dry_run", false),
		attribute.Bool("api_call_made", true),
	)

	// Update state
	c.updateRoomState(decision.RoomID, safeSetpoint, overrideEndTime)

	c.logExecutionInfo(span, decision.RoomName, "executing manual override - Netatmo API call successful, override applied",
		zap.Float64("setpoint", safeSetpoint),
		zap.Time("override_end_time", overrideEndTime.In(c.warsawLocation())),
		zap.Duration("override_duration", overrideDuration))
}

// applyConfigSafetyLimits applies configured safety limits to the setpoint
func (c *Controller) applyConfigSafetyLimits(setpoint float64, roomName string) (float64, bool) {
	safeSetpoint, clamped := applyConfiguredSafetyLimits(setpoint, c.config.MinSetpointCelsius, c.config.MaxSetpointCelsius)

	if clamped {
		c.logger.Warn("calculated setpoint clamped by safety limits",
			zap.String("room_name", roomName),
			zap.Float64("calculated_setpoint", setpoint),
			zap.Float64("min_setpoint", c.config.MinSetpointCelsius),
			zap.Float64("max_setpoint", c.config.MaxSetpointCelsius),
			zap.Float64("clamped_to", safeSetpoint))
	}

	return safeSetpoint, clamped
}

// setNetatmoThermostat calls Netatmo API to set thermostat setpoint
func (c *Controller) setNetatmoThermostat(ctx context.Context, roomID, roomName string, setpoint float64, endTime int64) error {
	durationMinutes := calculateOverrideDurationMinutes(endTime)

	ctx, span := c.tracer.Start(ctx, "set_netatmo_thermostat_"+roomName,
		trace.WithAttributes(
			attribute.String("home_id", c.homeID),
			attribute.String("room_id", roomID),
			attribute.Float64("setpoint", setpoint),
			attribute.String("mode", "manual"),
			attribute.Int64("duration_minutes", durationMinutes),
		),
	)
	defer span.End()

	c.logExecutionDebug(span, roomName, "calling Netatmo API SetRoomThermpoint",
		zap.String("home_id", c.homeID),
		zap.String("room_id", roomID),
		zap.Float64("setpoint", setpoint),
		zap.String("mode", "manual"),
		zap.Int64("duration_minutes", durationMinutes))

	err := c.netatmoClient.SetRoomThermpoint(ctx, c.homeID, roomID, "manual", setpoint, durationMinutes)

	recordAPICallAttributes(span, c.homeID, roomID, setpoint, durationMinutes, err == nil)

	if err != nil {
		recordError(span, err, "failed to set thermostat setpoint")
		c.logExecutionError(span, roomName, "Netatmo API SetRoomThermpoint failed", err)
		return err
	}

	c.logExecutionDebug(span, roomName, "Netatmo API SetRoomThermpoint successful")

	return nil
}

// updateRoomState updates the room state after setting override
// With the new simplified architecture, we don't need to track setpoint history
// State is derived fresh from API responses each cycle
func (c *Controller) updateRoomState(roomID string, setpoint float64, endTime time.Time) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if _, exists := c.stateByRoom[roomID]; exists {
		// State is now minimal - we don't track history
		// All information is re-derived from API responses
	}
}
