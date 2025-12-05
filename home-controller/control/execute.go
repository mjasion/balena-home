package control

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// executeDecision executes the control decision
func (c *Controller) executeDecision(ctx context.Context, decision ControlDecision) {
	ctx, span := c.tracer.Start(ctx, "execute_decision_"+decision.RoomName,
		trace.WithAttributes(
			attribute.String("room_name", decision.RoomName),
			attribute.String("room_id", decision.RoomID),
			attribute.String("action", decision.Action),
		),
	)
	defer span.End()

	c.logger.Debug("executing decision",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", decision.RoomName),
		zap.String("action", decision.Action),
		zap.String("reason", decision.Reason),
	)

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
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", decision.RoomName),
		zap.String("action", decision.Action),
		zap.String("reason", decision.Reason),
		zap.Float64("xiaomi_temp", decision.XiaomiTemperature),
		zap.Float64("scheduled_temp", decision.ScheduledTemp),
		zap.Float64("setpoint_temp", decision.SetpointTemperature),
		zap.Float64("thermostat_measured", decision.ThermostatMeasured),
		zap.String("thermostat_mode", decision.ThermostatMode),
	}

	if c.config.DryRun {
		c.logger.Info("[DRY-RUN] would NOT set temperature", logFields...)
	} else {
		c.logger.Info("control decision", logFields...)
	}
}

// executeManualOverride executes a manual override decision
func (c *Controller) executeManualOverride(ctx context.Context, span trace.Span, decision ControlDecision) {
	c.logger.Debug("executing manual override - applying safety limits",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", decision.RoomName),
		zap.Float64("calculated_setpoint", decision.CalculatedSetpoint),
	)

	// Apply safety limits
	safeSetpoint, clamped := c.applyConfigSafetyLimits(decision.CalculatedSetpoint, decision.RoomName)

	overrideEndTime := time.Unix(decision.OverrideEndTime, 0)
	overrideDuration := time.Until(overrideEndTime)

	span.SetAttributes(
		attribute.Float64("calculated_setpoint", decision.CalculatedSetpoint),
		attribute.Float64("safe_setpoint", safeSetpoint),
		attribute.Bool("clamped", clamped),
		attribute.Int64("override_duration_minutes", int64(overrideDuration.Minutes())),
	)

	if clamped {
		c.logger.Warn("executing manual override - setpoint clamped by safety limits",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", decision.RoomName),
			zap.Float64("original", decision.CalculatedSetpoint),
			zap.Float64("clamped_to", safeSetpoint),
		)
	}

	// Dry-run mode: log what would be sent but don't call API
	if c.config.DryRun {
		c.logger.Info("[DRY-RUN] executing manual override - WOULD set temperature (not sent to API)",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", decision.RoomName),
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
			zap.Duration("override_duration", overrideDuration),
		)

		span.SetAttributes(
			attribute.Bool("dry_run", true),
			attribute.Bool("api_call_made", false),
		)

		// Update state even in dry-run mode (for testing override extension)
		c.updateRoomState(decision.RoomID, safeSetpoint, overrideEndTime)
		return
	}

	c.logger.Info("executing manual override - preparing to call Netatmo API",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", decision.RoomName),
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
		zap.Duration("override_duration", overrideDuration),
	)

	// Call Netatmo API
	err := c.setNetatmoThermostat(ctx, decision.RoomID, decision.RoomName, safeSetpoint, decision.OverrideEndTime)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to set thermostat setpoint")
		span.SetAttributes(attribute.Bool("api_call_success", false))

		c.logger.Error("executing manual override - Netatmo API call failed",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", decision.RoomName),
			zap.Error(err),
		)
		return
	}

	span.SetAttributes(
		attribute.Bool("api_call_success", true),
		attribute.Bool("dry_run", false),
		attribute.Bool("api_call_made", true),
	)

	// Update state
	c.updateRoomState(decision.RoomID, safeSetpoint, overrideEndTime)

	c.logger.Info("executing manual override - Netatmo API call successful, override applied",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", decision.RoomName),
		zap.Float64("setpoint", safeSetpoint),
		zap.Time("override_end_time", overrideEndTime.In(c.warsawLocation())),
		zap.Duration("override_duration", overrideDuration),
	)
}

// applyConfigSafetyLimits applies configured safety limits to the setpoint
func (c *Controller) applyConfigSafetyLimits(setpoint float64, roomName string) (float64, bool) {
	clamped := false
	safeSetpoint := setpoint

	if safeSetpoint < c.config.MinSetpointCelsius {
		c.logger.Warn("calculated setpoint below safety limit, clamping to minimum",
			zap.String("room_name", roomName),
			zap.Float64("calculated_setpoint", setpoint),
			zap.Float64("min_setpoint", c.config.MinSetpointCelsius),
		)
		safeSetpoint = c.config.MinSetpointCelsius
		clamped = true
	} else if safeSetpoint > c.config.MaxSetpointCelsius {
		c.logger.Warn("calculated setpoint above safety limit, clamping to maximum",
			zap.String("room_name", roomName),
			zap.Float64("calculated_setpoint", setpoint),
			zap.Float64("max_setpoint", c.config.MaxSetpointCelsius),
		)
		safeSetpoint = c.config.MaxSetpointCelsius
		clamped = true
	}

	return safeSetpoint, clamped
}

// setNetatmoThermostat calls Netatmo API to set thermostat setpoint
func (c *Controller) setNetatmoThermostat(ctx context.Context, roomID, roomName string, setpoint float64, endTime int64) error {
	durationMinutes := int64((time.Unix(endTime, 0).Sub(time.Now())).Minutes())
	if durationMinutes <= 0 {
		durationMinutes = 1 // Minimum 1 minute
	}

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

	c.logger.Debug("calling Netatmo API SetRoomThermpoint",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
		zap.String("home_id", c.homeID),
		zap.String("room_id", roomID),
		zap.Float64("setpoint", setpoint),
		zap.String("mode", "manual"),
		zap.Int64("duration_minutes", durationMinutes),
	)

	err := c.netatmoClient.SetRoomThermpoint(ctx, c.homeID, roomID, "manual", setpoint, durationMinutes)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to set thermostat setpoint")
		span.SetAttributes(attribute.Bool("api_success", false))

		c.logger.Error("Netatmo API SetRoomThermpoint failed",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", roomName),
			zap.Error(err),
		)
		return err
	}

	span.SetAttributes(attribute.Bool("api_success", true))

	c.logger.Debug("Netatmo API SetRoomThermpoint successful",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
	)

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
