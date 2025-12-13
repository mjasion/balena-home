package control

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// spanHelper wraps span creation with consistent patterns
type spanHelper struct {
	tracer trace.Tracer
	logger *zap.Logger
}

// newSpanHelper creates a new span helper
func (c *Controller) newSpanHelper() *spanHelper {
	return &spanHelper{
		tracer: c.tracer,
		logger: c.logger,
	}
}

// startRoomSpan starts a span for room operations
func (c *Controller) startRoomSpan(ctx context.Context, operation, roomName, roomID string) (context.Context, trace.Span) {
	return c.tracer.Start(ctx, operation+"_"+roomName,
		trace.WithAttributes(
			attribute.String("room_name", roomName),
			attribute.String("room_id", roomID),
		),
	)
}

// startControlJobSpan starts the main control job span
func (c *Controller) startControlJobSpan(ctx context.Context) (context.Context, trace.Span) {
	return c.tracer.Start(ctx, "control_job",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("job", "control_job"),
			attribute.String("operation", "evaluate_and_control_rooms"),
			attribute.Int("mapping_count", len(c.config.Mappings)),
			attribute.Bool("dry_run", c.config.DryRun),
		),
	)
}

// recordSkipReason records why a room was skipped
func recordSkipReason(span trace.Span, reason string) {
	span.SetAttributes(attribute.String("skip_reason", reason))
}

// recordDecisionAttributes records control decision attributes on span
func recordDecisionAttributes(span trace.Span, decision ControlDecision, hardOverrideActive bool) {
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
}

// recordExecutionAttributes records execution attributes on span
func recordExecutionAttributes(span trace.Span, safeSetpoint float64, clamped bool, overrideDuration time.Duration, dryRun, apiSuccess bool) {
	span.SetAttributes(
		attribute.Float64("calculated_setpoint", safeSetpoint),
		attribute.Float64("safe_setpoint", safeSetpoint),
		attribute.Bool("clamped", clamped),
		attribute.Int64("override_duration_minutes", int64(overrideDuration.Minutes())),
		attribute.Bool("dry_run", dryRun),
		attribute.Bool("api_call_made", !dryRun),
		attribute.Bool("api_call_success", apiSuccess),
	)
}

// recordAPICallAttributes records Netatmo API call attributes
func recordAPICallAttributes(span trace.Span, homeID, roomID string, setpoint float64, durationMinutes int64, success bool) {
	span.SetAttributes(
		attribute.String("home_id", homeID),
		attribute.String("room_id", roomID),
		attribute.Float64("setpoint", setpoint),
		attribute.String("mode", "manual"),
		attribute.Int64("duration_minutes", durationMinutes),
		attribute.Bool("api_success", success),
	)
}

// recordError records an error on span with proper status
func recordError(span trace.Span, err error, message string) {
	span.RecordError(err)
	span.SetStatus(codes.Error, message)
}

// logRoomProcessing logs room processing events
func (c *Controller) logRoomProcessing(level func(string, ...zap.Field), traceID, roomName, message string, fields ...zap.Field) {
	allFields := []zap.Field{
		zap.String("trace_id", traceID),
		zap.String("room_name", roomName),
	}
	allFields = append(allFields, fields...)
	level(message, allFields...)
}

// logEvaluation logs evaluation decisions
func (c *Controller) logEvaluation(span trace.Span, roomName, message string, fields ...zap.Field) {
	allFields := []zap.Field{
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
	}
	allFields = append(allFields, fields...)
	c.logger.Info(message, allFields...)
}

// logEvaluationDebug logs debug-level evaluation info
func (c *Controller) logEvaluationDebug(span trace.Span, roomName, message string, fields ...zap.Field) {
	allFields := []zap.Field{
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
	}
	allFields = append(allFields, fields...)
	c.logger.Debug(message, allFields...)
}

// logEvaluationWarn logs warning-level evaluation info
func (c *Controller) logEvaluationWarn(span trace.Span, roomName, message string, fields ...zap.Field) {
	allFields := []zap.Field{
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
	}
	allFields = append(allFields, fields...)
	c.logger.Warn(message, allFields...)
}

// logExecutionInfo logs execution information
func (c *Controller) logExecutionInfo(span trace.Span, roomName, message string, fields ...zap.Field) {
	allFields := []zap.Field{
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
	}
	allFields = append(allFields, fields...)
	c.logger.Info(message, allFields...)
}

// logExecutionDebug logs execution debug info
func (c *Controller) logExecutionDebug(span trace.Span, roomName, message string, fields ...zap.Field) {
	allFields := []zap.Field{
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
	}
	allFields = append(allFields, fields...)
	c.logger.Debug(message, allFields...)
}

// logExecutionWarn logs execution warnings
func (c *Controller) logExecutionWarn(span trace.Span, roomName, message string, fields ...zap.Field) {
	allFields := []zap.Field{
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
	}
	allFields = append(allFields, fields...)
	c.logger.Warn(message, allFields...)
}

// logExecutionError logs execution errors
func (c *Controller) logExecutionError(span trace.Span, roomName, message string, err error, fields ...zap.Field) {
	allFields := []zap.Field{
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("room_name", roomName),
		zap.Error(err),
	}
	allFields = append(allFields, fields...)
	c.logger.Error(message, allFields...)
}
