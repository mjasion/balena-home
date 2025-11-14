package control

import (
	"context"
	"fmt"
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
	c.switchRoomsToScheduleMode(ctx, initialHomeStatus, &skipCount)

	// Step 2: Poll until all rooms confirmed in schedule mode
	c.pollUntilAllRoomsSynced(ctx)

	// Update last sync time BEFORE final status fetch (fail-safe)
	c.lastSyncTime = time.Now()

	// Step 3: Fetch final home status for all rooms
	roomStatusMap, err := c.fetchHomeStatus(ctx)
	if err != nil {
		c.logger.Error("failed to fetch final home status after sync", zap.Error(err))
		return skipCount, adjustCount, noAdjustCount
	}

	// Step 4: Evaluate and execute for each room (sequential)
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

// switchRoomsToScheduleMode switches all rooms to schedule mode (sequential)
// Optimized: only switches rooms that aren't already in schedule mode
// Sequential to avoid rate limiting
func (c *Controller) switchRoomsToScheduleMode(ctx context.Context, homeStatus *netatmo.HomeStatusResponse, skipCount *int) {
	// Convert homeStatus to roomStatusMap
	roomStatusMap := make(map[string]*netatmo.RoomStatus)
	for i := range homeStatus.Body.Home.Rooms {
		room := &homeStatus.Body.Home.Rooms[i]
		roomStatusMap[room.ID] = room
	}

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
		if roomStatus, ok := roomStatusMap[mapping.RoomID]; ok && roomStatus.ThermSetpointMode == "schedule" {
			c.logger.Debug("room already in schedule mode, skipping switch",
				zap.String("room_name", mapping.RoomName),
				zap.String("room_id", mapping.RoomID),
			)
			continue
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
}

// pollUntilAllRoomsSynced polls until all rooms are confirmed in schedule mode
func (c *Controller) pollUntilAllRoomsSynced(ctx context.Context) {
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
					state.SyncedScheduledTemp = roomStatus.ThermSetpointTemperature
					state.SyncedScheduledTime = time.Now()
					roomsSynced[roomStatus.ID] = true
					c.logger.Info("schedule synced for room",
						zap.String("room_id", roomStatus.ID),
						zap.String("room_name", state.RoomName),
						zap.Float64("scheduled_temp", roomStatus.ThermSetpointTemperature),
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
