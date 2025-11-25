package control

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// runSyncMode executes the control loop in SYNC MODE (schedule sync needed)
func (c *Controller) runSyncMode(ctx context.Context, initialHomeStatus *netatmo.HomeStatusResponse) (skipCount, adjustCount, noAdjustCount int) {
	ctx, span := c.tracer.Start(ctx, "sync_mode")
	defer span.End()

	c.logger.Info("schedule sync needed, using sync mode",
		zap.Int("rooms_count", len(c.config.Mappings)),
	)

	// Step 1: Switch all rooms to schedule mode (sequential)
	// Pass initial home status to avoid redundant API call
	// Returns map of previous overrides to restore after reading schedule
	previousOverrides := c.switchRoomsToScheduleMode(ctx, initialHomeStatus, &skipCount)

	// Step 2: Poll until all rooms confirmed in schedule mode
	// This reads and stores the scheduled temperatures
	// Also marks which rooms had schedule changes in previousOverrides map
	c.pollUntilAllRoomsSynced(ctx, previousOverrides)

	// Step 3: Restore previous overrides for rooms where schedule did NOT change
	// For rooms with schedule changes, skip restore and let evaluation handle it
	c.restorePreviousOverrides(ctx, previousOverrides)

	// Update last sync time AFTER restoring overrides (fail-safe)
	c.lastSyncTime = time.Now()

	// Step 4: Fetch final home status for all rooms
	roomStatusMap, err := c.fetchHomeStatus(ctx)
	if err != nil {
		c.logger.Error("failed to fetch final home status after sync", zap.Error(err))
		return skipCount, adjustCount, noAdjustCount
	}

	// Step 5: Evaluate and execute for each room (sequential)
	// Note: Rooms with restored overrides should skip evaluation (no adjustment needed)
	skip, adjust, noAdjust := c.evaluateAndExecuteRooms(ctx, roomStatusMap)
	return skipCount + skip, adjust, noAdjust
}

// runNormalMode executes the control loop in NORMAL MODE (no sync needed)
func (c *Controller) runNormalMode(ctx context.Context, homeStatus *netatmo.HomeStatusResponse) (skipCount, adjustCount, noAdjustCount int) {
	ctx, span := c.tracer.Start(ctx, "normal_mode")
	defer span.End()

	c.logger.Debug("schedule sync not needed, using normal control loop")

	// Convert homeStatus to roomStatusMap
	roomStatusMap := make(map[string]*netatmo.RoomStatus)
	for i := range homeStatus.Body.Home.Rooms {
		room := &homeStatus.Body.Home.Rooms[i]
		roomStatusMap[room.ID] = room
	}

	// Evaluate and execute for each room (sequential)
	return c.evaluateAndExecuteRooms(ctx, roomStatusMap)
}

// PreviousOverride stores override state before schedule sync
type PreviousOverride struct {
	RoomID           string
	Setpoint         float64
	EndTime          time.Time
	WasActive        bool
	ScheduleChanged  bool  // True if scheduled temperature changed during sync
}

// switchRoomsToScheduleMode switches all rooms to schedule mode (sequential)
// Optimized: only switches rooms that aren't already in schedule mode
// Sequential to avoid rate limiting
// Returns map of previous overrides to restore after sync
func (c *Controller) switchRoomsToScheduleMode(ctx context.Context, homeStatus *netatmo.HomeStatusResponse, skipCount *int) map[string]*PreviousOverride {
	// Convert homeStatus to roomStatusMap
	roomStatusMap := make(map[string]*netatmo.RoomStatus)
	for i := range homeStatus.Body.Home.Rooms {
		room := &homeStatus.Body.Home.Rooms[i]
		roomStatusMap[room.ID] = room
	}

	// Track previous overrides to restore after sync
	previousOverrides := make(map[string]*PreviousOverride)

	for _, mapping := range c.config.Mappings {
		// Skip externally modified rooms
		c.stateMu.RLock()
		state, exists := c.stateByRoom[mapping.RoomID]
		externallyModified := exists && state != nil && state.ExternallyModified
		c.stateMu.RUnlock()

		if externallyModified {
			c.logger.Debug("skipping schedule sync for externally modified room",
				zap.String("room_name", mapping.RoomName),
			)
			*skipCount++
			continue
		}

		// Check if room is already in schedule mode
		roomStatus, roomExists := roomStatusMap[mapping.RoomID]
		if roomExists && roomStatus.ThermSetpointMode == "schedule" {
			c.logger.Debug("room already in schedule mode, skipping switch",
				zap.String("room_name", mapping.RoomName),
				zap.String("room_id", mapping.RoomID),
			)
			continue
		}

		// Check if room has an active override to restore later
		if roomExists && roomStatus.ThermSetpointMode == "manual" {
			c.stateMu.RLock()
			state, exists := c.stateByRoom[mapping.RoomID]
			c.stateMu.RUnlock()

			// Check if this is our recent override (we set it within last 15 minutes)
			isOurOverride := false
			if exists && state != nil && !state.LastSetpointTime.IsZero() {
				timeSinceLastCommand := time.Since(state.LastSetpointTime)
				setpointDelta := math.Abs(roomStatus.ThermSetpointTemperature - state.LastSetpoint)

				if timeSinceLastCommand < 15*time.Minute && setpointDelta < 0.3 {
					// This is our override, we can reset it to schedule
					isOurOverride = true

					// Store override to restore after reading schedule
					previousOverrides[mapping.RoomID] = &PreviousOverride{
						RoomID:    mapping.RoomID,
						Setpoint:  roomStatus.ThermSetpointTemperature,
						EndTime:   state.OverrideEndTime,
						WasActive: true,
					}
					c.logger.Debug("stored previous override to restore after sync",
						zap.String("room_name", mapping.RoomName),
						zap.Float64("setpoint", roomStatus.ThermSetpointTemperature),
						zap.Time("end_time", state.OverrideEndTime),
					)
				}
			}

			if !isOurOverride {
				// Manual mode not set by algorithm - skip sync to respect user's manual setting
				c.logger.Info("skipping schedule sync for manual mode not set by algorithm",
					zap.String("room_name", mapping.RoomName),
					zap.String("room_id", mapping.RoomID),
					zap.Float64("current_setpoint", roomStatus.ThermSetpointTemperature),
				)
				*skipCount++
				continue
			}
		}

		if !c.config.DryRun {
			err := c.netatmoClient.SetRoomThermpoint(ctx, c.homeID, mapping.RoomID, "home", 0, 0)
			if err != nil {
				c.logger.Error("failed to switch room to schedule mode",
					zap.String("room_name", mapping.RoomName),
					zap.Error(err),
				)
			} else {
				c.logger.Debug("switched room to schedule mode",
					zap.String("room_name", mapping.RoomName),
					zap.String("room_id", mapping.RoomID),
				)
			}
		}
	}

	return previousOverrides
}

// pollUntilAllRoomsSynced polls until all rooms are confirmed in schedule mode
// Returns map tracking which rooms had schedule changes
func (c *Controller) pollUntilAllRoomsSynced(ctx context.Context, previousOverrides map[string]*PreviousOverride) {
	pollInterval := time.Duration(c.config.ScheduleSyncPollIntervalSeconds) * time.Second
	pollTimeout := time.Duration(c.config.ScheduleSyncPollTimeoutSeconds) * time.Second
	deadline := time.Now().Add(pollTimeout)

	roomsSynced := make(map[string]bool)

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		// Single GetHomeStatus call for all rooms
		homeStatus, err := c.netatmoClient.GetHomeStatus(ctx, c.homeID)
		if err != nil {
			c.logger.Warn("failed to fetch home status during sync poll", zap.Error(err))
			continue
		}

		// Check all rooms in single response
		for i := range homeStatus.Body.Home.Rooms {
			roomStatus := &homeStatus.Body.Home.Rooms[i]
			if roomStatus.ThermSetpointMode == "schedule" && !roomsSynced[roomStatus.ID] {
				// Room confirmed in schedule mode - store synced temperature
				c.stateMu.Lock()
				if state, exists := c.stateByRoom[roomStatus.ID]; exists {
					previousScheduledTemp := state.SyncedScheduledTemp
					newScheduledTemp := roomStatus.ThermSetpointTemperature

					// Check if schedule changed (more than 0.1°C difference)
					scheduleChanged := false
					if previousScheduledTemp > 0 && math.Abs(newScheduledTemp-previousScheduledTemp) > 0.1 {
						scheduleChanged = true
						c.logger.Info("schedule temperature changed",
							zap.String("room_id", roomStatus.ID),
							zap.String("room_name", state.RoomName),
							zap.Float64("previous_scheduled_temp", previousScheduledTemp),
							zap.Float64("new_scheduled_temp", newScheduledTemp),
						)
					}

					// Mark schedule change in state to bypass delayed execution
					state.ScheduleJustChanged = scheduleChanged

					// Mark schedule change in previousOverrides map
					if override, exists := previousOverrides[roomStatus.ID]; exists {
						override.ScheduleChanged = scheduleChanged
					}

					state.SyncedScheduledTemp = newScheduledTemp
					state.SyncedScheduledTime = time.Now()
					roomsSynced[roomStatus.ID] = true
					c.logger.Info("schedule synced for room",
						zap.String("room_id", roomStatus.ID),
						zap.String("room_name", state.RoomName),
						zap.Float64("scheduled_temp", newScheduledTemp),
						zap.Bool("schedule_changed", scheduleChanged),
					)
				}
				c.stateMu.Unlock()
			}
		}

		// Check if all rooms synced
		allSynced := true
		for _, mapping := range c.config.Mappings {
			c.stateMu.RLock()
			state, exists := c.stateByRoom[mapping.RoomID]
			externallyModified := exists && state != nil && state.ExternallyModified
			c.stateMu.RUnlock()

			if !externallyModified && !roomsSynced[mapping.RoomID] {
				allSynced = false
				break
			}
		}

		if allSynced {
			c.logger.Info("all rooms synced successfully")
			break
		}
	}
}

// restorePreviousOverrides restores manual overrides that were active before schedule sync
// Only restores overrides for rooms where schedule did NOT change
func (c *Controller) restorePreviousOverrides(ctx context.Context, previousOverrides map[string]*PreviousOverride) {
	if len(previousOverrides) == 0 {
		return
	}

	c.logger.Info("checking previous overrides after schedule sync",
		zap.Int("total_count", len(previousOverrides)),
	)

	for roomID, override := range previousOverrides {
		if !override.WasActive {
			continue
		}

		// Skip restoration if schedule changed - let evaluation handle it with fresh schedule
		if override.ScheduleChanged {
			c.logger.Info("schedule changed, skipping restore (will re-evaluate)",
				zap.String("room_id", roomID),
			)
			continue
		}

		// Calculate remaining duration
		remainingDuration := time.Until(override.EndTime)

		// If override expired or expires very soon (< 2 minutes), skip restore
		// This prevents "Endtime in the past" API errors during schedule sync delays
		// The control loop will re-evaluate and create a new override if still needed
		if remainingDuration < 2*time.Minute {
			c.logger.Info("override expired or expires soon, skipping restore (will re-evaluate)",
				zap.String("room_id", roomID),
				zap.Time("end_time", override.EndTime),
				zap.Duration("remaining_duration", remainingDuration),
			)
			continue
		}

		// Get room name for logging
		var roomName string
		c.stateMu.RLock()
		if state, exists := c.stateByRoom[roomID]; exists {
			roomName = state.RoomName
		}
		c.stateMu.RUnlock()

		if !c.config.DryRun {
			// Restore manual override with remaining duration
			durationMinutes := int64(remainingDuration.Minutes())
			err := c.netatmoClient.SetRoomThermpoint(ctx, c.homeID, roomID, "manual", override.Setpoint, durationMinutes)
			if err != nil {
				c.logger.Error("failed to restore previous override",
					zap.String("room_name", roomName),
					zap.String("room_id", roomID),
					zap.Float64("setpoint", override.Setpoint),
					zap.Error(err),
				)
			} else {
				c.logger.Info("restored previous override after schedule sync",
					zap.String("room_name", roomName),
					zap.String("room_id", roomID),
					zap.Float64("setpoint", override.Setpoint),
					zap.Duration("remaining_duration", remainingDuration),
				)
			}
		} else {
			c.logger.Info("[DRY-RUN] WOULD restore previous override",
				zap.String("room_name", roomName),
				zap.String("room_id", roomID),
				zap.Float64("setpoint", override.Setpoint),
				zap.Duration("remaining_duration", remainingDuration),
			)
		}
	}
}

// fetchHomeStatus fetches current Netatmo home status and returns room status map
func (c *Controller) fetchHomeStatus(ctx context.Context) (map[string]*netatmo.RoomStatus, error) {
	ctx, span := c.tracer.Start(ctx, "fetch_netatmo_home_status",
		trace.WithAttributes(attribute.String("home_id", c.homeID)),
	)
	defer span.End()

	homeStatus, err := c.netatmoClient.GetHomeStatus(ctx, c.homeID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch home status: %w", err)
	}

	span.SetAttributes(attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)))

	// Build map of room ID to room status
	roomStatusMap := make(map[string]*netatmo.RoomStatus)
	for i := range homeStatus.Body.Home.Rooms {
		room := &homeStatus.Body.Home.Rooms[i]
		roomStatusMap[room.ID] = room
	}

	return roomStatusMap, nil
}

// shouldSyncSchedule checks if schedule sync is needed at specific minutes of the hour
// For interval=15: syncs at :00, :15, :30, :45
// For interval=30: syncs at :00, :30
// For interval=60: syncs at :00
func (c *Controller) shouldSyncSchedule() bool {
	if c.config.ScheduleSyncIntervalMinutes <= 0 {
		return false
	}

	now := time.Now()
	currentMinute := now.Minute()

	// Check if current minute is a sync point (e.g., :00, :15, :30, :45 for interval=15)
	if currentMinute%c.config.ScheduleSyncIntervalMinutes != 0 {
		return false
	}

	// Avoid multiple syncs in the same minute window
	// If last sync was within the last minute, skip
	if time.Since(c.lastSyncTime) < time.Minute {
		return false
	}

	return true
}
