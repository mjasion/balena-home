package control

import (
	"context"
	"fmt"
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
	ctx, span := h.tracer.Start(ctx, "hard_override_job",
		trace.WithAttributes(
			attribute.Int("override_count", len(h.controller.config.HardOverrides)),
		),
	)
	defer span.End()

	h.logger.Debug("hard override job started",
		zap.Int("override_count", len(h.controller.config.HardOverrides)),
	)

	// If no hard overrides configured, nothing to do
	if len(h.controller.config.HardOverrides) == 0 {
		return
	}

	// Fetch current home status
	homeStatus, err := h.controller.netatmoClient.GetHomeStatus(ctx, h.controller.homeID)
	if err != nil {
		h.logger.Error("failed to fetch home status for hard override job",
			zap.Error(err),
		)
		span.RecordError(err)
		return
	}

	span.SetAttributes(
		attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)

	// Build room status map for quick lookup
	roomStatusMap := make(map[string]*netatmo.RoomStatus)
	for i := range homeStatus.Body.Home.Rooms {
		room := &homeStatus.Body.Home.Rooms[i]
		roomStatusMap[room.ID] = room
	}

	// Check each hard override
	for _, override := range h.controller.config.HardOverrides {
		// Check if this override is currently active
		targetTemp, isActive := h.controller.getHardOverrideTemp(override)
		if !isActive {
			// Override not active at current time
			continue
		}

		h.logger.Debug("hard override is active",
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
			h.logger.Warn("no mapping found for hard override room",
				zap.String("room_name", override.RoomName),
			)
			continue
		}

		// Get room status
		roomStatus, roomExists := roomStatusMap[roomID]
		if !roomExists {
			h.logger.Warn("room not found in home status",
				zap.String("room_name", override.RoomName),
				zap.String("room_id", roomID),
			)
			continue
		}

		if !roomStatus.Reachable {
			h.logger.Debug("room not reachable",
				zap.String("room_name", override.RoomName),
			)
			continue
		}

		// Check if there's a human override (>= 60 minutes) - skip if so
		if h.controller.isHumanOverride(roomStatus) {
			h.logger.Debug("skipping hard override due to active human override",
				zap.String("room_name", override.RoomName),
			)
			continue
		}

		// Check if override duration exceeds 15 minutes (not our override)
		if h.isNonAlgorithmicOverride(roomStatus) {
			h.logger.Debug("skipping hard override due to non-algorithmic override",
				zap.String("room_name", override.RoomName),
			)
			continue
		}

		// Calculate setpoint for this room using three-zone algorithm with hard override target
		calculatedSetpoint := h.calculateSetpointForHardOverride(ctx, override, roomStatus, h.controller.config.Mappings)

		// Check if setpoint already matches current (no change needed)
		if math.Abs(calculatedSetpoint-roomStatus.ThermSetpointTemperature) < 0.1 {
			h.logger.Debug("hard override: setpoint already at target",
				zap.String("room_name", override.RoomName),
				zap.Float64("target_setpoint", calculatedSetpoint),
			)
			continue
		}

		// Execute the override
		if !h.controller.config.DryRun {
			// Set override with boundary-aligned end time
			endTime := h.controller.calculateBoundaryAlignedEndTime()
			durationMinutes := int64(endTime.Sub(time.Now()).Minutes())
			if durationMinutes <= 0 {
				durationMinutes = 1 // Minimum 1 minute
			}

			err := h.controller.netatmoClient.SetRoomThermpoint(
				ctx,
				h.controller.homeID,
				roomID,
				"manual",
				calculatedSetpoint,
				durationMinutes,
			)

			if err != nil {
				h.logger.Error("failed to set hard override",
					zap.String("room_name", override.RoomName),
					zap.Error(err),
				)
			} else {
				h.logger.Info("hard override applied",
					zap.String("room_name", override.RoomName),
					zap.Float64("calculated_setpoint", calculatedSetpoint),
					zap.Float64("target_temperature", targetTemp),
					zap.Duration("override_duration", time.Duration(durationMinutes)*time.Minute),
				)
			}
		} else {
			h.logger.Info("[DRY-RUN] WOULD apply hard override",
				zap.String("room_name", override.RoomName),
				zap.Float64("calculated_setpoint", calculatedSetpoint),
				zap.Float64("target_temperature", targetTemp),
			)
		}
	}

	h.logger.Debug("hard override job completed")
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
	calculatedSetpoint = h.controller.applySafetyBounds(calculatedSetpoint)

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
