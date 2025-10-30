package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ContextLogger wraps a zap logger and adds trace context to log entries
type ContextLogger struct {
	*zap.Logger
}

// NewContextLogger creates a new ContextLogger that wraps the given logger
func NewContextLogger(logger *zap.Logger) *ContextLogger {
	return &ContextLogger{Logger: logger}
}

// WithTraceContext returns a logger with trace context fields if available
// This adds trace_id and span_id to all subsequent log entries
func (l *ContextLogger) WithTraceContext(ctx context.Context) *zap.Logger {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return l.Logger
	}

	spanContext := span.SpanContext()
	return l.Logger.With(
		zap.String("trace_id", spanContext.TraceID().String()),
		zap.String("span_id", spanContext.SpanID().String()),
	)
}

// LogWithTrace is a helper function that logs with trace context
func LogWithTrace(ctx context.Context, logger *zap.Logger, level zapcore.Level, msg string, fields ...zap.Field) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		spanContext := span.SpanContext()
		fields = append(fields,
			zap.String("trace_id", spanContext.TraceID().String()),
			zap.String("span_id", spanContext.SpanID().String()),
		)
	}

	switch level {
	case zapcore.DebugLevel:
		logger.Debug(msg, fields...)
	case zapcore.InfoLevel:
		logger.Info(msg, fields...)
	case zapcore.WarnLevel:
		logger.Warn(msg, fields...)
	case zapcore.ErrorLevel:
		logger.Error(msg, fields...)
	default:
		logger.Info(msg, fields...)
	}
}

// InfoWithTrace logs at info level with trace context
func InfoWithTrace(ctx context.Context, logger *zap.Logger, msg string, fields ...zap.Field) {
	LogWithTrace(ctx, logger, zapcore.InfoLevel, msg, fields...)
}

// DebugWithTrace logs at debug level with trace context
func DebugWithTrace(ctx context.Context, logger *zap.Logger, msg string, fields ...zap.Field) {
	LogWithTrace(ctx, logger, zapcore.DebugLevel, msg, fields...)
}

// WarnWithTrace logs at warn level with trace context
func WarnWithTrace(ctx context.Context, logger *zap.Logger, msg string, fields ...zap.Field) {
	LogWithTrace(ctx, logger, zapcore.WarnLevel, msg, fields...)
}

// ErrorWithTrace logs at error level with trace context
func ErrorWithTrace(ctx context.Context, logger *zap.Logger, msg string, fields ...zap.Field) {
	LogWithTrace(ctx, logger, zapcore.ErrorLevel, msg, fields...)
}

// TraceCore is a custom zapcore.Core that automatically adds trace context
type TraceCore struct {
	zapcore.Core
	ctx context.Context
}

// NewTraceCore wraps a zapcore.Core to automatically add trace context
func NewTraceCore(core zapcore.Core, ctx context.Context) *TraceCore {
	return &TraceCore{
		Core: core,
		ctx:  ctx,
	}
}

// With adds structured context to the core
func (tc *TraceCore) With(fields []zapcore.Field) zapcore.Core {
	return &TraceCore{
		Core: tc.Core.With(fields),
		ctx:  tc.ctx,
	}
}

// Check determines whether the supplied Entry should be logged
func (tc *TraceCore) Check(entry zapcore.Entry, checkedEntry *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if tc.Enabled(entry.Level) {
		return checkedEntry.AddCore(entry, tc)
	}
	return checkedEntry
}

// Write serializes the Entry and any Fields supplied at the log site
func (tc *TraceCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// Add trace context fields if available
	span := trace.SpanFromContext(tc.ctx)
	if span.IsRecording() {
		spanContext := span.SpanContext()
		fields = append(fields,
			zap.String("trace_id", spanContext.TraceID().String()),
			zap.String("span_id", spanContext.SpanID().String()),
		)
	}

	return tc.Core.Write(entry, fields)
}
