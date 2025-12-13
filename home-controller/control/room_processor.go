package control

import (
	"context"
	"sync"
	"time"

	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// ProcessingCounts tracks the results of room processing
type ProcessingCounts struct {
	mu            sync.Mutex
	skipCount     int
	adjustCount   int
	noAdjustCount int
}

// incrementSkip increments the skip counter
func (pc *ProcessingCounts) incrementSkip() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.skipCount++
}

// incrementAdjust increments the adjust counter
func (pc *ProcessingCounts) incrementAdjust() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.adjustCount++
}

// incrementNoAdjust increments the no-adjust counter
func (pc *ProcessingCounts) incrementNoAdjust() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.noAdjustCount++
}

// getCounts returns the current counts
func (pc *ProcessingCounts) getCounts() (skip, adjust, noAdjust int) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.skipCount, pc.adjustCount, pc.noAdjustCount
}

// processRoomsConcurrently processes all rooms concurrently with per-room waiting for manual mode expiration
func (c *Controller) processRoomsConcurrently(ctx context.Context, initialRoomStatusMap map[string]*netatmo.RoomStatus) (skipCount, adjustCount, noAdjustCount int) {
	ctx, span := c.tracer.Start(ctx, "process_rooms_concurrently",
		trace.WithAttributes(
			attribute.Int("total_rooms", len(c.config.Mappings)),
		),
	)
	defer span.End()

	var wg sync.WaitGroup
	counts := &ProcessingCounts{}

	c.logger.Debug("spawning concurrent goroutines for room processing",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("goroutine_count", len(c.config.Mappings)),
	)

	for _, mapping := range c.config.Mappings {
		wg.Add(1)
		go c.processRoom(ctx, mapping, initialRoomStatusMap, &wg, counts)
	}

	c.logger.Debug("waiting for all room processing goroutines to complete",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
	)

	wg.Wait()

	skipCount, adjustCount, noAdjustCount = counts.getCounts()

	span.SetAttributes(
		attribute.Int("total_skipped", skipCount),
		attribute.Int("total_adjusted", adjustCount),
		attribute.Int("total_no_adjustment", noAdjustCount),
	)

	c.logger.Debug("all room processing goroutines completed",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("skipped", skipCount),
		zap.Int("adjusted", adjustCount),
		zap.Int("no_adjustment", noAdjustCount),
	)

	return skipCount, adjustCount, noAdjustCount
}

// processRoom handles a single room (goroutine worker)
func (c *Controller) processRoom(ctx context.Context, mapping config.ThermostatMapping,
	initialRoomStatusMap map[string]*netatmo.RoomStatus, wg *sync.WaitGroup, counts *ProcessingCounts) {
	defer wg.Done()

	// Get room status from initial fetch
	roomStatus, roomExists := initialRoomStatusMap[mapping.RoomID]
	if !roomExists {
		c.logger.Warn("room processing - room not found in home status",
			zap.String("room_name", mapping.RoomName),
			zap.String("room_id", mapping.RoomID),
		)
		counts.incrementSkip()
		return
	}

	c.logger.Debug("room processing started",
		zap.String("room_name", mapping.RoomName),
		zap.String("mode", roomStatus.ThermSetpointMode),
		zap.Float64("current_setpoint", roomStatus.ThermSetpointTemperature),
		zap.Float64("measured_temp", roomStatus.ThermMeasuredTemperature),
	)

	// Wait for override expiration if needed
	roomStatus = c.waitForOverrideExpirationIfNeeded(ctx, mapping, roomStatus, counts)
	if roomStatus == nil {
		// Context cancelled or error occurred
		return
	}

	// Evaluate room with current or fresh status
	decision := c.evaluateRoom(ctx, mapping, map[string]*netatmo.RoomStatus{mapping.RoomID: roomStatus})

	c.logger.Debug("room processing - evaluation completed",
		zap.String("room_name", mapping.RoomName),
		zap.String("action", decision.Action),
		zap.String("reason", decision.Reason),
	)

	// Track decision type
	switch decision.Action {
	case "skip":
		counts.incrementSkip()
	case "set_manual_override":
		counts.incrementAdjust()
	case "no_adjustment_needed":
		counts.incrementNoAdjust()
	}

	// Execute decision
	c.executeDecision(ctx, decision)

	c.logger.Debug("room processing completed",
		zap.String("room_name", mapping.RoomName),
		zap.String("final_action", decision.Action),
	)
}

// waitForOverrideExpirationIfNeeded waits for algorithm-set override to expire if within window
// Returns updated room status or nil if context cancelled/error
func (c *Controller) waitForOverrideExpirationIfNeeded(ctx context.Context, mapping config.ThermostatMapping,
	roomStatus *netatmo.RoomStatus, counts *ProcessingCounts) *netatmo.RoomStatus {

	// Check if room is in manual mode with algorithm-set override expiring within window
	if roomStatus.ThermSetpointMode != "manual" || !c.shouldWaitForOverrideExpiration(roomStatus) {
		return roomStatus
	}

	expirationTime := time.Unix(roomStatus.ThermSetpointEndTime, 0)
	waitDuration := time.Until(expirationTime.Add(1 * time.Second))

	c.logger.Info("room processing - waiting for algorithm-set override to expire",
		zap.String("room_name", mapping.RoomName),
		zap.Time("override_end_time", expirationTime),
		zap.Duration("wait_duration", waitDuration),
	)

	// Wait for override to expire
	if waitDuration > 0 {
		select {
		case <-time.After(waitDuration):
			// Override expired, fetch fresh status
			return c.fetchFreshRoomStatus(ctx, mapping, counts)

		case <-ctx.Done():
			c.logger.Warn("room processing - context cancelled while waiting for override expiration",
				zap.String("room_name", mapping.RoomName),
			)
			counts.incrementSkip()
			return nil
		}
	}

	return roomStatus
}

// fetchFreshRoomStatus fetches fresh home status and returns the specific room
func (c *Controller) fetchFreshRoomStatus(ctx context.Context, mapping config.ThermostatMapping, counts *ProcessingCounts) *netatmo.RoomStatus {
	c.logger.Info("room processing - override expired, fetching fresh home status",
		zap.String("room_name", mapping.RoomName),
	)

	freshStatus, err := c.netatmoClient.GetHomeStatus(ctx, c.homeID)
	if err != nil {
		c.logger.Error("room processing - failed to fetch fresh home status after override expiration",
			zap.String("room_name", mapping.RoomName),
			zap.Error(err),
		)
		counts.incrementSkip()
		return nil
	}

	// Find this room in fresh status
	for i := range freshStatus.Body.Home.Rooms {
		if freshStatus.Body.Home.Rooms[i].ID == mapping.RoomID {
			roomStatus := &freshStatus.Body.Home.Rooms[i]
			c.logger.Debug("room processing - updated to fresh room status",
				zap.String("room_name", mapping.RoomName),
				zap.String("new_mode", roomStatus.ThermSetpointMode),
				zap.Float64("new_setpoint", roomStatus.ThermSetpointTemperature),
			)
			return roomStatus
		}
	}

	c.logger.Warn("room processing - room not found in fresh status",
		zap.String("room_name", mapping.RoomName),
	)
	counts.incrementSkip()
	return nil
}

// shouldWaitForOverrideExpiration checks if we should wait for the override to expire
// Returns true if override expires within the next 15 minutes (current window)
func (c *Controller) shouldWaitForOverrideExpiration(roomStatus *netatmo.RoomStatus) bool {
	if roomStatus.ThermSetpointEndTime == 0 {
		return false
	}

	// Check if this is a human override (>= 60 minutes)
	if c.isHumanOverride(roomStatus) {
		return false
	}

	// Calculate when the override expires
	expirationTime := time.Unix(roomStatus.ThermSetpointEndTime, 0)
	now := time.Now()
	timeUntilExpiration := expirationTime.Sub(now)

	// Wait only if expiration is within next 15 minutes
	// This ensures we're in the same 15-minute window as Control Job execution
	return timeUntilExpiration > 0 && timeUntilExpiration <= 15*time.Minute
}
