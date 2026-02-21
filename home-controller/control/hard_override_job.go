package control

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	homeOtel "github.com/mjasion/balena-home/thermostats/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// HardOverrideJob handles time-based temperature overrides independently from Control Job
type HardOverrideJob struct {
	controller       *Controller
	logger           *zap.Logger
	tracer           trace.Tracer
	sharedHomeStatus *SharedHomeStatus
}

// NewHardOverrideJob creates a new hard override job
func NewHardOverrideJob(controller *Controller, logger *zap.Logger, tracer trace.Tracer, sharedHomeStatus *SharedHomeStatus) *HardOverrideJob {
	return &HardOverrideJob{
		controller:       controller,
		logger:           logger,
		tracer:           tracer,
		sharedHomeStatus: sharedHomeStatus,
	}
}

// Run executes the hard override job (called by scheduler every minute)
func (h *HardOverrideJob) Run(ctx context.Context) {
	// Create a new root trace span for this job execution
	ctx, span := h.tracer.Start(ctx, "hard_override_job",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("job", "hard_override_job"),
			attribute.String("operation", "apply_time_based_overrides"),
			attribute.Int("override_count", len(h.controller.config.HardOverrides)),
		),
	)
	defer span.End()

	h.logger.Info("hard override job started - checking for active time-based overrides",
		homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
		zap.Int("configured_overrides", len(h.controller.config.HardOverrides)),
	)

	// If no hard overrides configured, nothing to do
	if len(h.controller.config.HardOverrides) == 0 {
		h.logger.Debug("hard override job - no overrides configured, skipping")
		span.SetAttributes(attribute.Bool("has_overrides", false))
		return
	}

	span.SetAttributes(attribute.Bool("has_overrides", true))

	// Get home status from shared state (set by Metric Job) or fetch fresh
	const maxDataAge = 30 * time.Second
	var homeStatus *netatmo.HomeStatusResponse

	sharedStatus, age, metricJobTraceID, hasData := h.sharedHomeStatus.Get()
	if hasData && age <= maxDataAge {
		homeStatus = sharedStatus
		h.logger.Debug("hard override job - using fresh data from metric job",
			homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
			zap.String("metric_job_trace_id", metricJobTraceID),
			zap.Duration("data_age", age),
		)
		span.SetAttributes(
			attribute.String("data_source", "metric_job"),
			attribute.String("metric_job_trace_id", metricJobTraceID),
		)
	} else {
		if hasData {
			h.logger.Debug("hard override job - shared data is stale, fetching fresh",
				homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
				zap.Duration("data_age", age),
			)
		} else {
			h.logger.Debug("hard override job - no shared data, fetching from Netatmo API",
				homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
			)
		}

		fetchedStatus, err := h.controller.netatmoClient.GetHomeStatus(ctx, h.controller.homeID)
		if err != nil {
			h.logger.Error("hard override job failed - could not fetch home status",
				zap.Error(err),
				homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
			)
			span.RecordError(err)
			span.SetAttributes(attribute.Bool("fetch_success", false))
			return
		}
		homeStatus = fetchedStatus
		span.SetAttributes(attribute.String("data_source", "direct_fetch"))
	}

	span.SetAttributes(
		attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		attribute.Bool("fetch_success", true),
	)

	h.logger.Debug("hard override job - home status available",
		homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)

	// Build room status map for quick lookup
	roomStatusMap := make(map[string]*netatmo.RoomStatus)
	for i := range homeStatus.Body.Home.Rooms {
		room := &homeStatus.Body.Home.Rooms[i]
		roomStatusMap[room.ID] = room
	}

	// Check each hard override
	activeOverrideCount := 0
	appliedOverrideCount := 0
	skippedOverrideCount := 0

	for _, override := range h.controller.config.HardOverrides {
		// Check if this override is currently active
		targetTemp, isActive := h.controller.getHardOverrideTemp(override)
		if !isActive {
			// Override not active at current time
			h.logger.Debug("hard override job - override not active at current time",
				zap.String("room_name", override.RoomName),
			)
			continue
		}

		activeOverrideCount++

		h.logger.Info("hard override job - active override detected",
			homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
			zap.String("room_name", override.RoomName),
			zap.Float64("target_temperature", targetTemp),
		)

		// Find the room ID for this override
		var roomID string
		for _, mapping := range h.controller.config.Mappings {
			if mapping.RoomName == override.RoomName {
				roomID = mapping.RoomID
				break
			}
		}

		if roomID == "" {
			h.logger.Warn("hard override job - no mapping found for override room",
				zap.String("room_name", override.RoomName),
			)
			skippedOverrideCount++
			continue
		}

		// Get room status
		roomStatus, roomExists := roomStatusMap[roomID]
		if !roomExists {
			h.logger.Warn("hard override job - room not found in home status",
				zap.String("room_name", override.RoomName),
				zap.String("room_id", roomID),
			)
			skippedOverrideCount++
			continue
		}

		if !roomStatus.Reachable {
			h.logger.Info("hard override job - room not reachable, skipping",
				zap.String("room_name", override.RoomName),
			)
			skippedOverrideCount++
			continue
		}

		// Check if there's a human override (>= 60 minutes) - skip if so
		if h.controller.isHumanOverride(roomStatus) {
			h.logger.Info("hard override job - skipping due to active human override (duration >= 60 min)",
				zap.String("room_name", override.RoomName),
				zap.String("mode", roomStatus.ThermSetpointMode),
			)
			skippedOverrideCount++
			continue
		}

		// Check if override duration exceeds 15 minutes (not our override)
		if h.isNonAlgorithmicOverride(roomStatus) {
			h.logger.Info("hard override job - skipping due to non-algorithmic override (duration > 15 min)",
				zap.String("room_name", override.RoomName),
			)
			skippedOverrideCount++
			continue
		}

		// Calculate setpoint for this room using three-zone algorithm with hard override target
		// Only applies when room is below target (Xiaomi sensor)
		calculatedSetpoint, shouldApply := h.calculateSetpointForHardOverride(ctx, override, roomStatus, h.controller.config.Mappings)
		if !shouldApply {
			skippedOverrideCount++
			continue
		}

		// Check if setpoint already matches current (no change needed)
		if math.Abs(calculatedSetpoint-roomStatus.ThermSetpointTemperature) < 0.1 {
			h.logger.Info("hard override job - setpoint already at target, no change needed",
				zap.String("room_name", override.RoomName),
				zap.Float64("target_setpoint", calculatedSetpoint),
				zap.Float64("current_setpoint", roomStatus.ThermSetpointTemperature),
			)
			skippedOverrideCount++
			continue
		}

		// Execute the override
		if !h.controller.config.DryRun {
			// Set override with configured duration
			endTime := time.Now().Add(time.Duration(h.controller.config.OverrideDurationMinutes) * time.Minute)

			h.logger.Info("hard override job - applying override via Netatmo API",
				zap.String("room_name", override.RoomName),
				zap.Float64("calculated_setpoint", calculatedSetpoint),
				zap.Float64("target_temperature", targetTemp),
				zap.Time("override_end_time", endTime),
			)

			err := h.controller.netatmoClient.SetRoomThermpoint(
				ctx,
				h.controller.homeID,
				roomID,
				"manual",
				calculatedSetpoint,
				endTime.Unix(),
			)

			if err != nil {
				h.logger.Error("hard override job - failed to apply override",
					homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
					zap.String("room_name", override.RoomName),
					zap.Error(err),
				)
			} else {
				h.logger.Info("hard override job - override applied successfully",
					homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
					zap.String("room_name", override.RoomName),
					zap.Float64("calculated_setpoint", calculatedSetpoint),
					zap.Float64("target_temperature", targetTemp),
					zap.Time("override_end_time", endTime),
				)
				appliedOverrideCount++
			}
		} else {
			h.logger.Info("[DRY-RUN] hard override job - WOULD apply override (not sent to API)",
				zap.String("room_name", override.RoomName),
				zap.Float64("calculated_setpoint", calculatedSetpoint),
				zap.Float64("target_temperature", targetTemp),
			)
			appliedOverrideCount++ // Count as applied for dry-run tracking
		}
	}

	span.SetAttributes(
		attribute.Int("active_overrides", activeOverrideCount),
		attribute.Int("applied_overrides", appliedOverrideCount),
		attribute.Int("skipped_overrides", skippedOverrideCount),
	)

	h.logger.Info("hard override job completed",
		homeOtel.TraceField(ctx), homeOtel.LogContext(ctx),
		zap.Int("active_overrides", activeOverrideCount),
		zap.Int("applied_overrides", appliedOverrideCount),
		zap.Int("skipped_overrides", skippedOverrideCount),
	)
}

// calculateSetpointForHardOverride calculates the setpoint using the three-zone algorithm with hard override target.
// Returns (setpoint, shouldApply). shouldApply is false when room is already at/above target (Xiaomi) or no sensor data.
func (h *HardOverrideJob) calculateSetpointForHardOverride(
	ctx context.Context,
	override config.HardOverride,
	roomStatus *netatmo.RoomStatus,
	mappings []config.ThermostatMapping,
) (float64, bool) {
	// Find sensor MAC for this room
	var sensorMAC string
	for _, mapping := range mappings {
		if mapping.RoomName == override.RoomName {
			sensorMAC = strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
			break
		}
	}

	// Get Xiaomi temperature if available
	xiaomiTemp := 0.0
	if sensorMAC != "" {
		temp, err := h.controller.getWeightedAverageTemperature(ctx, sensorMAC)
		if err == nil && temp != 0 {
			xiaomiTemp = temp
		}
	}

	// Get target temperature from hard override
	targetTemp, isActive := h.controller.getHardOverrideTemp(override)
	if !isActive {
		// This shouldn't happen since we already checked, but safety
		return roomStatus.ThermSetpointTemperature, false
	}

	// If we don't have Xiaomi data, skip — cannot make informed decisions without sensor data
	if xiaomiTemp == 0 {
		h.logger.Warn("no Xiaomi data available for hard override, skipping",
			zap.String("room_name", override.RoomName),
		)
		return 0, false
	}

	// Hard override only applies when room is below target temperature (Xiaomi sensor)
	// If room is already at or above target, let normal schedule/manual mode handle it
	if xiaomiTemp >= targetTemp {
		h.logger.Info("hard override job - room already at/above target, skipping override",
			zap.String("room_name", override.RoomName),
			zap.Float64("xiaomi_temp", xiaomiTemp),
			zap.Float64("target_temp", targetTemp),
		)
		return 0, false
	}

	// Room is below target — calculate setpoint using three-zone algorithm
	tempDiff := xiaomiTemp - targetTemp

	var calculatedSetpoint float64

	switch {
	case tempDiff <= -h.controller.config.TemperatureThreshold:
		// Too cold - add 0.5°C
		calculatedSetpoint = roomStatus.ThermMeasuredTemperature + 0.5

	default:
		// Within range (between -threshold and 0, since we already checked xiaomiTemp < targetTemp)
		calculatedSetpoint = roomStatus.ThermMeasuredTemperature
	}

	// Round and apply safety bounds
	calculatedSetpoint = roundToHalfDegree(calculatedSetpoint)
	calculatedSetpoint = h.controller.applySafetyBounds(calculatedSetpoint)

	h.logger.Debug("hard override setpoint calculation",
		zap.String("room_name", override.RoomName),
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("override_target", targetTemp),
		zap.Float64("temp_diff", tempDiff),
		zap.Float64("calculated_setpoint", calculatedSetpoint),
	)

	return calculatedSetpoint, true
}

// isNonAlgorithmicOverride checks if there's a non-algorithmic override active
// This would be indicated by an override duration exceeding our configured duration + 5 min buffer
func (h *HardOverrideJob) isNonAlgorithmicOverride(roomStatus *netatmo.RoomStatus) bool {
	if roomStatus.ThermSetpointMode != "manual" {
		return false
	}

	if roomStatus.ThermSetpointEndTime == 0 {
		return false
	}

	// Calculate duration
	durationSeconds := roomStatus.ThermSetpointEndTime - roomStatus.ThermSetpointStartTime
	// Non-algorithmic if duration exceeds configured override duration + 5 min buffer
	maxAlgorithmicDuration := int64(h.controller.config.OverrideDurationMinutes+5) * 60
	return durationSeconds > maxAlgorithmicDuration
}
