package control

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// HardOverrideJob handles time-based temperature overrides independently from Control Job
type HardOverrideJob struct {
	controller *Controller
	logger     *zap.Logger
	tracer     trace.Tracer
}

// NewHardOverrideJob creates a new hard override job
func NewHardOverrideJob(controller *Controller, logger *zap.Logger, tracer trace.Tracer) *HardOverrideJob {
	return &HardOverrideJob{
		controller: controller,
		logger:     logger,
		tracer:     tracer,
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
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("configured_overrides", len(h.controller.config.HardOverrides)),
	)

	// If no hard overrides configured, nothing to do
	if len(h.controller.config.HardOverrides) == 0 {
		h.logger.Debug("hard override job - no overrides configured, skipping")
		span.SetAttributes(attribute.Bool("has_overrides", false))
		return
	}

	span.SetAttributes(attribute.Bool("has_overrides", true))

	// Fetch current home status
	h.logger.Debug("hard override job - fetching home status from Netatmo API",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
	)

	homeStatus, err := h.controller.netatmoClient.GetHomeStatus(ctx, h.controller.homeID)
	if err != nil {
		h.logger.Error("hard override job failed - could not fetch home status",
			zap.Error(err),
			zap.String("trace_id", span.SpanContext().TraceID().String()),
		)
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("fetch_success", false))
		return
	}

	span.SetAttributes(
		attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		attribute.Bool("fetch_success", true),
	)

	h.logger.Debug("hard override job - home status fetched successfully",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
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
			zap.String("trace_id", span.SpanContext().TraceID().String()),
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
		calculatedSetpoint := h.calculateSetpointForHardOverride(ctx, override, roomStatus, h.controller.config.Mappings)

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
			// Set override with boundary-aligned end time
			endTime := calculateBoundaryAlignedEndTime()
			durationMinutes := int64(endTime.Sub(time.Now()).Minutes())
			if durationMinutes <= 0 {
				durationMinutes = 1 // Minimum 1 minute
			}

			h.logger.Info("hard override job - applying override via Netatmo API",
				zap.String("room_name", override.RoomName),
				zap.Float64("calculated_setpoint", calculatedSetpoint),
				zap.Float64("target_temperature", targetTemp),
				zap.Duration("override_duration", time.Duration(durationMinutes)*time.Minute),
			)

			err := h.controller.netatmoClient.SetRoomThermpoint(
				ctx,
				h.controller.homeID,
				roomID,
				"manual",
				calculatedSetpoint,
				durationMinutes,
			)

			if err != nil {
				h.logger.Error("hard override job - failed to apply override",
					zap.String("trace_id", span.SpanContext().TraceID().String()),
					zap.String("room_name", override.RoomName),
					zap.Error(err),
				)
			} else {
				h.logger.Info("hard override job - override applied successfully",
					zap.String("trace_id", span.SpanContext().TraceID().String()),
					zap.String("room_name", override.RoomName),
					zap.Float64("calculated_setpoint", calculatedSetpoint),
					zap.Float64("target_temperature", targetTemp),
					zap.Duration("override_duration", time.Duration(durationMinutes)*time.Minute),
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
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("active_overrides", activeOverrideCount),
		zap.Int("applied_overrides", appliedOverrideCount),
		zap.Int("skipped_overrides", skippedOverrideCount),
	)
}

// calculateSetpointForHardOverride calculates the setpoint using the three-zone algorithm with hard override target
func (h *HardOverrideJob) calculateSetpointForHardOverride(
	ctx context.Context,
	override config.HardOverride,
	roomStatus *netatmo.RoomStatus,
	mappings []config.ThermostatMapping,
) float64 {
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
		return roomStatus.ThermSetpointTemperature
	}

	// If we don't have Xiaomi data, just use measured + adjustment
	if xiaomiTemp == 0 {
		h.logger.Warn("no Xiaomi data available for hard override, using thermostat measured",
			zap.String("room_name", override.RoomName),
		)
		// Return measured temperature to maintain stability
		return roundToHalfDegree(roomStatus.ThermMeasuredTemperature)
	}

	// Calculate temperature difference
	tempDiff := xiaomiTemp - targetTemp

	// Apply three-zone algorithm
	var calculatedSetpoint float64

	switch {
	case tempDiff <= -h.controller.config.TemperatureThreshold:
		// Too cold - add 0.5°C
		calculatedSetpoint = roomStatus.ThermMeasuredTemperature + 0.5

	case tempDiff >= h.controller.config.TemperatureThreshold:
		// Too warm - subtract 0.5°C
		calculatedSetpoint = roomStatus.ThermMeasuredTemperature - 0.5

	default:
		// Within range - maintain current reading
		calculatedSetpoint = roomStatus.ThermMeasuredTemperature
	}

	// Round and apply safety bounds
	calculatedSetpoint = roundToHalfDegree(calculatedSetpoint)
	calculatedSetpoint = applySafetyBounds(calculatedSetpoint)

	h.logger.Debug("hard override setpoint calculation",
		zap.String("room_name", override.RoomName),
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("override_target", targetTemp),
		zap.Float64("temp_diff", tempDiff),
		zap.Float64("calculated_setpoint", calculatedSetpoint),
	)

	return calculatedSetpoint
}

// isNonAlgorithmicOverride checks if there's a non-algorithmic override active
// This would be indicated by an override duration > 15 minutes
func (h *HardOverrideJob) isNonAlgorithmicOverride(roomStatus *netatmo.RoomStatus) bool {
	if roomStatus.ThermSetpointMode != "manual" {
		return false
	}

	if roomStatus.ThermSetpointEndTime == 0 {
		return false
	}

	// Calculate duration
	durationSeconds := roomStatus.ThermSetpointEndTime - roomStatus.ThermSetpointStartTime
	// More than 15 minutes (900 seconds)
	return durationSeconds > 15*60
}
