package otel

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TraceField returns a zap field with trace_id extracted from the span in ctx.
// This provides backward-compatible trace_id in stdout/Loki logs.
// Returns zap.Skip() when no valid span context exists.
func TraceField(ctx context.Context) zap.Field {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	if !sc.IsValid() {
		return zap.Skip()
	}
	return zap.String("trace_id", sc.TraceID().String())
}

// LogContext returns a zap field that carries the context for the otelzap bridge.
// The otelzap bridge extracts trace_id and span_id from the context automatically
// and sets them as proper OTLP log record fields for native trace correlation.
//
// This field uses SkipType so it is invisible in standard encoders (stdout, JSON, logfmt)
// but the otelzap bridge detects it via field.Interface.(context.Context) type assertion.
func LogContext(ctx context.Context) zap.Field {
	return zap.Field{
		Key:       "otel.context",
		Type:      zapcore.SkipType,
		Interface: ctx,
	}
}
