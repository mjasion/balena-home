package otel

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap/zapcore"
)

func TestTraceField_WithValidSpan(t *testing.T) {
	// Create a tracer and start a span to get a valid span context
	tracer := noop.NewTracerProvider().Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	field := TraceField(ctx)

	// noop tracer produces invalid span context, so TraceField returns Skip
	// This tests the no-span path with noop tracer
	if field.Type != zapcore.SkipType {
		t.Errorf("Expected SkipType for noop tracer span, got %v", field.Type)
	}
}

func TestTraceField_WithNoSpan(t *testing.T) {
	ctx := context.Background()

	field := TraceField(ctx)

	if field.Type != zapcore.SkipType {
		t.Errorf("Expected SkipType for context without span, got %v", field.Type)
	}
}

func TestTraceField_WithRealTraceID(t *testing.T) {
	// Create a span context with a real trace ID
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	field := TraceField(ctx)

	if field.Key != "trace_id" {
		t.Errorf("Expected key 'trace_id', got '%s'", field.Key)
	}
	if field.Type != zapcore.StringType {
		t.Errorf("Expected StringType, got %v", field.Type)
	}
	if field.String != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("Expected trace ID '4bf92f3577b34da6a3ce929d0e0e4736', got '%s'", field.String)
	}
}

func TestLogContext_CarriesContext(t *testing.T) {
	ctx := context.Background()

	field := LogContext(ctx)

	if field.Key != "otel.context" {
		t.Errorf("Expected key 'otel.context', got '%s'", field.Key)
	}
	if field.Type != zapcore.SkipType {
		t.Errorf("Expected SkipType, got %v", field.Type)
	}
	if field.Interface != ctx {
		t.Error("Expected field.Interface to be the original context")
	}
}

func TestLogContext_WithSpanContext(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	field := LogContext(ctx)

	// Verify the context can be extracted from the field
	extractedCtx, ok := field.Interface.(context.Context)
	if !ok {
		t.Fatal("Expected field.Interface to be a context.Context")
	}

	// Verify span context is preserved
	extractedSC := trace.SpanContextFromContext(extractedCtx)
	if extractedSC.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("Expected trace ID preserved, got '%s'", extractedSC.TraceID().String())
	}
}
