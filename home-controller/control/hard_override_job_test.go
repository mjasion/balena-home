package control

import (
	"context"
	"testing"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

// newTestController creates a minimal controller for testing with the given config and BLE readings
func newTestController(t *testing.T, cfg *config.ThermostatControlConfig, bleReadings []bleTestReading) *Controller {
	t.Helper()

	logger := zap.NewNop()
	controlBuffer := buffer.New(1000, logger)

	ctx := context.Background()
	now := time.Now()

	// Add BLE readings to control buffer
	for _, r := range bleReadings {
		controlBuffer.Add(ctx, &buffer.Reading{
			Type: buffer.ReadingTypeBLE,
			BLE: &buffer.SensorReading{
				Timestamp:          now.Add(-r.ageSeconds),
				MAC:                r.mac,
				TemperatureCelsius: r.temperature,
			},
		})
	}

	return &Controller{
		config:        cfg,
		controlBuffer: controlBuffer,
		logger:        logger,
		tracer:        noop.NewTracerProvider().Tracer("test"),
		stateByRoom:   make(map[string]*ThermostatState),
	}
}

type bleTestReading struct {
	mac        string
	temperature float64
	ageSeconds time.Duration
}

func TestCalculateSetpointForHardOverride_SkipsWhenXiaomiAboveTarget(t *testing.T) {
	cfg := &config.ThermostatControlConfig{
		TemperatureThreshold:    0.2,
		OverrideDurationMinutes: 30,
		MinSetpointCelsius:      10.0,
		MaxSetpointCelsius:      30.0,
		Mappings: []config.ThermostatMapping{
			{RoomName: "Łazienka", SensorMAC: "A4:C1:38:F1:2D:0D", RoomID: "room1"},
		},
		HardOverrides: []config.HardOverride{
			{
				RoomName: "Łazienka",
				Schedule: []config.HardOverrideWindow{
					{StartTime: "00:00", EndTime: "23:59", TargetTemperature: 24.0},
				},
			},
		},
	}

	controller := newTestController(t, cfg, []bleTestReading{
		{mac: "A4:C1:38:F1:2D:0D", temperature: 25.0, ageSeconds: 10 * time.Second},
	})

	job := &HardOverrideJob{
		controller: controller,
		logger:     zap.NewNop(),
	}

	roomStatus := &netatmo.RoomStatus{
		ID:                       "room1",
		Reachable:                true,
		ThermMeasuredTemperature: 23.0,
		ThermSetpointTemperature: 24.0,
		ThermSetpointMode:        "schedule",
	}

	override := cfg.HardOverrides[0]
	ctx := context.Background()

	_, shouldApply := job.calculateSetpointForHardOverride(ctx, override, roomStatus, cfg.Mappings)

	if shouldApply {
		t.Error("expected shouldApply=false when xiaomi temp (25.0) >= target (24.0)")
	}
}

func TestCalculateSetpointForHardOverride_AppliesWhenXiaomiBelowTarget(t *testing.T) {
	cfg := &config.ThermostatControlConfig{
		TemperatureThreshold:    0.2,
		OverrideDurationMinutes: 30,
		MinSetpointCelsius:      10.0,
		MaxSetpointCelsius:      30.0,
		Mappings: []config.ThermostatMapping{
			{RoomName: "Łazienka", SensorMAC: "A4:C1:38:F1:2D:0D", RoomID: "room1"},
		},
		HardOverrides: []config.HardOverride{
			{
				RoomName: "Łazienka",
				Schedule: []config.HardOverrideWindow{
					{StartTime: "00:00", EndTime: "23:59", TargetTemperature: 26.5},
				},
			},
		},
	}

	controller := newTestController(t, cfg, []bleTestReading{
		{mac: "A4:C1:38:F1:2D:0D", temperature: 22.0, ageSeconds: 10 * time.Second},
	})

	job := &HardOverrideJob{
		controller: controller,
		logger:     zap.NewNop(),
	}

	roomStatus := &netatmo.RoomStatus{
		ID:                       "room1",
		Reachable:                true,
		ThermMeasuredTemperature: 21.0,
		ThermSetpointTemperature: 20.0,
		ThermSetpointMode:        "schedule",
	}

	override := cfg.HardOverrides[0]
	ctx := context.Background()

	setpoint, shouldApply := job.calculateSetpointForHardOverride(ctx, override, roomStatus, cfg.Mappings)

	if !shouldApply {
		t.Fatal("expected shouldApply=true when xiaomi temp (22.0) < target (26.5)")
	}

	// xiaomi=22.0, target=26.5, diff=-4.5 which is <= -0.2 → too cold → measured+0.5 = 21.5
	expectedSetpoint := 21.5
	if setpoint != expectedSetpoint {
		t.Errorf("expected setpoint=%.1f, got=%.1f", expectedSetpoint, setpoint)
	}
}

func TestCalculateSetpointForHardOverride_SkipsWithNoSensorData(t *testing.T) {
	cfg := &config.ThermostatControlConfig{
		TemperatureThreshold:    0.2,
		OverrideDurationMinutes: 30,
		MinSetpointCelsius:      10.0,
		MaxSetpointCelsius:      30.0,
		Mappings: []config.ThermostatMapping{
			{RoomName: "Łazienka", SensorMAC: "A4:C1:38:F1:2D:0D", RoomID: "room1"},
		},
		HardOverrides: []config.HardOverride{
			{
				RoomName: "Łazienka",
				Schedule: []config.HardOverrideWindow{
					{StartTime: "00:00", EndTime: "23:59", TargetTemperature: 26.5},
				},
			},
		},
	}

	// No BLE readings → sensor data unavailable
	controller := newTestController(t, cfg, nil)

	job := &HardOverrideJob{
		controller: controller,
		logger:     zap.NewNop(),
	}

	roomStatus := &netatmo.RoomStatus{
		ID:                       "room1",
		Reachable:                true,
		ThermMeasuredTemperature: 21.0,
		ThermSetpointTemperature: 20.0,
		ThermSetpointMode:        "schedule",
	}

	override := cfg.HardOverrides[0]
	ctx := context.Background()

	_, shouldApply := job.calculateSetpointForHardOverride(ctx, override, roomStatus, cfg.Mappings)

	if shouldApply {
		t.Error("expected shouldApply=false when no sensor data available")
	}
}

func TestCalculateSetpointForHardOverride_PrecedenceOverScheduleMode(t *testing.T) {
	cfg := &config.ThermostatControlConfig{
		TemperatureThreshold:    0.2,
		OverrideDurationMinutes: 30,
		MinSetpointCelsius:      10.0,
		MaxSetpointCelsius:      30.0,
		Mappings: []config.ThermostatMapping{
			{RoomName: "Sypialnia", SensorMAC: "A4:C1:38:ED:C0:21", RoomID: "room2"},
		},
		HardOverrides: []config.HardOverride{
			{
				RoomName: "Sypialnia",
				Schedule: []config.HardOverrideWindow{
					{StartTime: "00:00", EndTime: "23:59", TargetTemperature: 23.0},
				},
			},
		},
	}

	// Xiaomi reads 20.0, target is 23.0 → should apply
	controller := newTestController(t, cfg, []bleTestReading{
		{mac: "A4:C1:38:ED:C0:21", temperature: 20.0, ageSeconds: 5 * time.Second},
	})

	job := &HardOverrideJob{
		controller: controller,
		logger:     zap.NewNop(),
	}

	// Room is in schedule mode with low setpoint — hard override should take precedence
	roomStatus := &netatmo.RoomStatus{
		ID:                       "room2",
		Reachable:                true,
		ThermMeasuredTemperature: 19.5,
		ThermSetpointTemperature: 18.0, // schedule has low temp
		ThermSetpointMode:        "schedule",
	}

	override := cfg.HardOverrides[0]
	ctx := context.Background()

	setpoint, shouldApply := job.calculateSetpointForHardOverride(ctx, override, roomStatus, cfg.Mappings)

	if !shouldApply {
		t.Fatal("expected hard override to take precedence over schedule mode when xiaomi (20.0) < target (23.0)")
	}

	// diff = 20.0 - 23.0 = -3.0, <= -0.2 → too cold → measured + 0.5 = 20.0
	expectedSetpoint := 20.0
	if setpoint != expectedSetpoint {
		t.Errorf("expected setpoint=%.1f, got=%.1f", expectedSetpoint, setpoint)
	}
}

func TestCalculateSetpointForHardOverride_PrecedenceOverManualMode(t *testing.T) {
	cfg := &config.ThermostatControlConfig{
		TemperatureThreshold:    0.2,
		OverrideDurationMinutes: 30,
		MinSetpointCelsius:      10.0,
		MaxSetpointCelsius:      30.0,
		Mappings: []config.ThermostatMapping{
			{RoomName: "Hol", SensorMAC: "A4:C1:38:26:E2:4C", RoomID: "room3"},
		},
		HardOverrides: []config.HardOverride{
			{
				RoomName: "Hol",
				Schedule: []config.HardOverrideWindow{
					{StartTime: "00:00", EndTime: "23:59", TargetTemperature: 24.0},
				},
			},
		},
	}

	// Xiaomi reads 21.5, target is 24.0 → should apply
	controller := newTestController(t, cfg, []bleTestReading{
		{mac: "A4:C1:38:26:E2:4C", temperature: 21.5, ageSeconds: 5 * time.Second},
	})

	job := &HardOverrideJob{
		controller: controller,
		logger:     zap.NewNop(),
	}

	now := time.Now()

	// Room has a short manual override (algorithm-set, < 35 min = configured + 5 buffer)
	roomStatus := &netatmo.RoomStatus{
		ID:                       "room3",
		Reachable:                true,
		ThermMeasuredTemperature: 21.0,
		ThermSetpointTemperature: 19.0, // manual mode has low setpoint
		ThermSetpointMode:        "manual",
		ThermSetpointStartTime:   now.Add(-10 * time.Minute).Unix(),
		ThermSetpointEndTime:     now.Add(20 * time.Minute).Unix(), // 30 min total
	}

	override := cfg.HardOverrides[0]
	ctx := context.Background()

	setpoint, shouldApply := job.calculateSetpointForHardOverride(ctx, override, roomStatus, cfg.Mappings)

	if !shouldApply {
		t.Fatal("expected hard override to take precedence over manual mode when xiaomi (21.5) < target (24.0)")
	}

	// diff = 21.5 - 24.0 = -2.5, <= -0.2 → too cold → measured + 0.5 = 21.5
	expectedSetpoint := 21.5
	if setpoint != expectedSetpoint {
		t.Errorf("expected setpoint=%.1f, got=%.1f", expectedSetpoint, setpoint)
	}
}

func TestCalculateSetpointForHardOverride_WithinRange(t *testing.T) {
	cfg := &config.ThermostatControlConfig{
		TemperatureThreshold:    0.2,
		OverrideDurationMinutes: 30,
		MinSetpointCelsius:      10.0,
		MaxSetpointCelsius:      30.0,
		Mappings: []config.ThermostatMapping{
			{RoomName: "Łazienka", SensorMAC: "A4:C1:38:F1:2D:0D", RoomID: "room1"},
		},
		HardOverrides: []config.HardOverride{
			{
				RoomName: "Łazienka",
				Schedule: []config.HardOverrideWindow{
					{StartTime: "00:00", EndTime: "23:59", TargetTemperature: 26.0},
				},
			},
		},
	}

	// Xiaomi reads 25.9, target is 26.0 → diff = -0.1, within range (-0.2, 0)
	controller := newTestController(t, cfg, []bleTestReading{
		{mac: "A4:C1:38:F1:2D:0D", temperature: 25.9, ageSeconds: 5 * time.Second},
	})

	job := &HardOverrideJob{
		controller: controller,
		logger:     zap.NewNop(),
	}

	roomStatus := &netatmo.RoomStatus{
		ID:                       "room1",
		Reachable:                true,
		ThermMeasuredTemperature: 25.0,
		ThermSetpointTemperature: 24.0,
		ThermSetpointMode:        "schedule",
	}

	override := cfg.HardOverrides[0]
	ctx := context.Background()

	setpoint, shouldApply := job.calculateSetpointForHardOverride(ctx, override, roomStatus, cfg.Mappings)

	if !shouldApply {
		t.Fatal("expected shouldApply=true when xiaomi (25.9) < target (26.0)")
	}

	// diff = 25.9 - 26.0 = -0.1, within range → maintain measured = 25.0
	expectedSetpoint := 25.0
	if setpoint != expectedSetpoint {
		t.Errorf("expected setpoint=%.1f, got=%.1f", expectedSetpoint, setpoint)
	}
}

func TestIsNonAlgorithmicOverride_WithConfiguredDuration(t *testing.T) {
	cfg := &config.ThermostatControlConfig{
		OverrideDurationMinutes: 30, // 30 min overrides
	}

	controller := &Controller{
		config: cfg,
		logger: zap.NewNop(),
	}

	job := &HardOverrideJob{
		controller: controller,
		logger:     zap.NewNop(),
	}

	now := time.Now()

	tests := []struct {
		name     string
		status   *netatmo.RoomStatus
		expected bool
	}{
		{
			name: "schedule mode - not non-algorithmic",
			status: &netatmo.RoomStatus{
				ThermSetpointMode: "schedule",
			},
			expected: false,
		},
		{
			name: "manual mode, no end time - not non-algorithmic",
			status: &netatmo.RoomStatus{
				ThermSetpointMode:    "manual",
				ThermSetpointEndTime: 0,
			},
			expected: false,
		},
		{
			name: "manual mode, 30 min duration (our override) - not non-algorithmic",
			status: &netatmo.RoomStatus{
				ThermSetpointMode:      "manual",
				ThermSetpointStartTime: now.Unix(),
				ThermSetpointEndTime:   now.Add(30 * time.Minute).Unix(),
			},
			expected: false,
		},
		{
			name: "manual mode, 34 min duration (close to configured+buffer) - not non-algorithmic",
			status: &netatmo.RoomStatus{
				ThermSetpointMode:      "manual",
				ThermSetpointStartTime: now.Unix(),
				ThermSetpointEndTime:   now.Add(34 * time.Minute).Unix(),
			},
			expected: false,
		},
		{
			name: "manual mode, 36 min duration (exceeds configured+5min buffer) - is non-algorithmic",
			status: &netatmo.RoomStatus{
				ThermSetpointMode:      "manual",
				ThermSetpointStartTime: now.Unix(),
				ThermSetpointEndTime:   now.Add(36 * time.Minute).Unix(),
			},
			expected: true,
		},
		{
			name: "manual mode, 45 min duration - is non-algorithmic",
			status: &netatmo.RoomStatus{
				ThermSetpointMode:      "manual",
				ThermSetpointStartTime: now.Unix(),
				ThermSetpointEndTime:   now.Add(45 * time.Minute).Unix(),
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := job.isNonAlgorithmicOverride(tt.status)
			if result != tt.expected {
				t.Errorf("isNonAlgorithmicOverride() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestEvaluateRoom_TooWarmThresholdHardcoded(t *testing.T) {
	cfg := &config.ThermostatControlConfig{
		TemperatureThreshold:    0.2,
		OverrideDurationMinutes: 30,
		MinSetpointCelsius:      10.0,
		MaxSetpointCelsius:      30.0,
		Mappings: []config.ThermostatMapping{
			{RoomName: "Salon", SensorMAC: "A4:C1:38:26:E2:4C", RoomID: "room1"},
		},
	}

	// Xiaomi reads 22.15, scheduled is 22.0 → diff = 0.15
	// With old threshold (0.2): within_range
	// With new threshold (0.1): too_warm
	controller := newTestController(t, cfg, []bleTestReading{
		{mac: "A4:C1:38:26:E2:4C", temperature: 22.15, ageSeconds: 5 * time.Second},
	})

	mapping := cfg.Mappings[0]
	roomStatusMap := map[string]*netatmo.RoomStatus{
		"room1": {
			ID:                       "room1",
			Reachable:                true,
			ThermMeasuredTemperature: 22.5,
			ThermSetpointTemperature: 22.0,
			ThermSetpointMode:        "manual",
			ThermSetpointStartTime:   time.Now().Add(-5 * time.Minute).Unix(),
			ThermSetpointEndTime:     time.Now().Add(25 * time.Minute).Unix(),
		},
	}

	ctx := context.Background()
	decision := controller.evaluateRoom(ctx, mapping, roomStatusMap)

	// tempDiff = 22.15 - 22.0 = 0.15, >= 0.1 → too_warm
	// calculatedSetpoint = measured - 0.5 = 22.5 - 0.5 = 22.0
	// currentSetpoint = 22.0, calculated = 22.0 → no_adjustment_needed
	if decision.Action != "no_adjustment_needed" {
		t.Errorf("expected action=no_adjustment_needed (too_warm zone with matching setpoint), got=%s reason=%s", decision.Action, decision.Reason)
	}
}
