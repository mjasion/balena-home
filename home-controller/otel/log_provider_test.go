package otel

import (
	"context"
	"testing"

	"github.com/mjasion/balena-home/thermostats/config"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.uber.org/zap"
)

func TestInitLogProvider_Disabled(t *testing.T) {
	cfg := &config.OpenTelemetryConfig{
		Enabled: false,
	}
	logger, _ := zap.NewDevelopment()
	res := resource.Default()

	provider, shutdown, err := InitLogProvider(context.Background(), cfg, res, logger)
	if err != nil {
		t.Fatalf("InitLogProvider() error = %v", err)
	}
	if provider != nil {
		t.Error("Expected nil provider when disabled")
	}
	if shutdown == nil {
		t.Fatal("Expected non-nil shutdown function")
	}

	// Shutdown should be no-op and not error
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() error = %v", err)
	}
}

func TestInitLogProvider_Enabled(t *testing.T) {
	cfg := &config.OpenTelemetryConfig{
		Enabled:     true,
		Endpoint:    "http://localhost:4318",
		ServiceName: "test-service",
	}
	logger, _ := zap.NewDevelopment()
	res := resource.Default()

	provider, shutdown, err := InitLogProvider(context.Background(), cfg, res, logger)
	if err != nil {
		t.Fatalf("InitLogProvider() error = %v", err)
	}
	if provider == nil {
		t.Fatal("Expected non-nil provider when enabled")
	}
	if shutdown == nil {
		t.Fatal("Expected non-nil shutdown function")
	}

	// Shutdown should work cleanly
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() error = %v", err)
	}
}

func TestInitLogProvider_WithHeaders(t *testing.T) {
	cfg := &config.OpenTelemetryConfig{
		Enabled:     true,
		Endpoint:    "https://tempo.example.com/otlp",
		ServiceName: "test-service",
		Headers:     "Authorization=Bearer token123,X-Custom=value",
	}
	logger, _ := zap.NewDevelopment()
	res := resource.Default()

	provider, shutdown, err := InitLogProvider(context.Background(), cfg, res, logger)
	if err != nil {
		t.Fatalf("InitLogProvider() error = %v", err)
	}
	if provider == nil {
		t.Fatal("Expected non-nil provider with headers")
	}

	if err := shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown() error = %v", err)
	}
}
