package control

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Controller manages the thermostat control loop
type Controller struct {
	config        *config.ThermostatControlConfig
	netatmoClient *netatmo.Client
	controlBuffer *buffer.RingBuffer
	metricsBuffer *buffer.RingBuffer // For pushing control metrics to Prometheus
	logger        *zap.Logger
	tracer        trace.Tracer

	// Cached Netatmo home ID (doesn't change)
	homeID string

	// State tracking (thread-safe with mutex)
	stateMu     sync.RWMutex
	stateByRoom map[string]*ThermostatState // Key: RoomID

	// Mapping of sensor MAC to room IDs
	sensorToRooms map[string][]string // Key: sensor MAC (uppercase), Value: list of room IDs

	// Shared home status for Metric Job → Control Job communication
	sharedHomeStatus *SharedHomeStatus
}

// New creates a new thermostat controller
func New(
	cfg *config.ThermostatControlConfig,
	netatmoClient *netatmo.Client,
	controlBuffer *buffer.RingBuffer,
	metricsBuffer *buffer.RingBuffer,
	logger *zap.Logger,
	sharedHomeStatus *SharedHomeStatus,
) *Controller {
	// Build sensor-to-rooms mapping
	sensorToRooms := make(map[string][]string)
	for _, mapping := range cfg.Mappings {
		mac := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
		roomID := mapping.RoomID
		if roomID == "" {
			// RoomID will be populated at runtime from Netatmo API
			roomID = mapping.RoomName // Use room name as placeholder for now
		}
		sensorToRooms[mac] = append(sensorToRooms[mac], roomID)
	}

	// Get tracer from global provider
	tracer := otel.Tracer("home-controller/control")

	return &Controller{
		config:           cfg,
		netatmoClient:    netatmoClient,
		controlBuffer:    controlBuffer,
		metricsBuffer:    metricsBuffer,
		logger:           logger,
		tracer:           tracer,
		stateByRoom:      make(map[string]*ThermostatState),
		sensorToRooms:    sensorToRooms,
		sharedHomeStatus: sharedHomeStatus,
	}
}

// Initialize initializes the controller state (must be called before Run)
func (c *Controller) Initialize(ctx context.Context) error {
	c.logger.Info("initializing thermostat controller",
		zap.Int("mapping_count", len(c.config.Mappings)),
		zap.String("metric_job_cron", c.config.MetricJobCron),
		zap.String("control_job_cron", c.config.ControlJobCron),
		zap.String("hard_override_job_cron", c.config.HardOverrideJobCron),
	)

	// Initialize state from Netatmo API (get room IDs)
	if err := c.initializeRoomIDs(ctx); err != nil {
		return fmt.Errorf("failed to initialize room IDs: %w", err)
	}

	c.logger.Info("thermostat controller initialized successfully")
	return nil
}

// Run executes a single control loop iteration (called by scheduler)
func (c *Controller) Run(ctx context.Context) {
	c.runControlLoop(ctx)
}

// initializeRoomIDs fetches Netatmo home data to populate room IDs for mappings
func (c *Controller) initializeRoomIDs(ctx context.Context) error {
	homesData, err := c.netatmoClient.GetHomesData(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch homes data: %w", err)
	}

	if len(homesData.Body.Homes) == 0 {
		return fmt.Errorf("no homes found in Netatmo account")
	}

	// Use first home (most users have only one)
	home := homesData.Body.Homes[0]

	// Cache homeID for future control loops
	c.homeID = home.ID

	c.logger.Info("initializing room IDs from Netatmo",
		zap.String("home_id", home.ID),
		zap.String("home_name", home.Name),
		zap.Int("room_count", len(home.Rooms)),
	)

	// Build map of room name to room ID
	roomNameToID := make(map[string]string)
	for _, room := range home.Rooms {
		roomNameToID[room.Name] = room.ID
	}

	// Update mappings with room IDs
	for i := range c.config.Mappings {
		mapping := &c.config.Mappings[i]
		roomID, found := roomNameToID[mapping.RoomName]
		if !found {
			return fmt.Errorf("room '%s' not found in Netatmo home (available rooms: %v)",
				mapping.RoomName, getMapKeys(roomNameToID))
		}
		mapping.RoomID = roomID

		// Initialize state for this room
		c.stateMu.Lock()
		if _, exists := c.stateByRoom[roomID]; !exists {
			c.stateByRoom[roomID] = &ThermostatState{
				RoomID:   roomID,
				RoomName: mapping.RoomName,
			}
		}
		c.stateMu.Unlock()

		c.logger.Info("initialized room mapping",
			zap.String("room_name", mapping.RoomName),
			zap.String("room_id", roomID),
			zap.String("sensor_mac", mapping.SensorMAC),
		)
	}

	// Rebuild sensor-to-rooms mapping with actual room IDs
	c.sensorToRooms = make(map[string][]string)
	for _, mapping := range c.config.Mappings {
		mac := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
		c.sensorToRooms[mac] = append(c.sensorToRooms[mac], mapping.RoomID)
	}

	return nil
}

// addToMetricsBuffer converts Netatmo home status to buffer readings and adds them
// Runs in a goroutine to avoid blocking control loop
func (c *Controller) addToMetricsBuffer(ctx context.Context, homeStatus *netatmo.HomeStatusResponse) {
	go func() {
		spanCtx, span := c.tracer.Start(ctx, "add_netatmo_to_metrics_buffer",
			trace.WithAttributes(
				attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
			),
		)
		defer span.End()
		_ = spanCtx // Context used only for span creation

		timestamp := time.Now()

		// Build room name map from config mappings
		roomNames := make(map[string]string)
		for _, mapping := range c.config.Mappings {
			roomNames[mapping.RoomID] = mapping.RoomName
		}

		readingsAdded := 0
		for _, roomStatus := range homeStatus.Body.Home.Rooms {
			// Get room name from mapping, fallback to room ID
			roomName, ok := roomNames[roomStatus.ID]
			if !ok {
				roomName = roomStatus.ID
			}

			// Create buffer reading
			bufferReading := &buffer.Reading{
				Type: buffer.ReadingTypeNetatmo,
				Thermostat: &buffer.ThermostatReading{
					Timestamp:           timestamp,
					HomeID:              c.homeID,
					HomeName:            "", // We don't have home name in HomeStatusResponse
					RoomID:              roomStatus.ID,
					RoomName:            roomName,
					MeasuredTemperature: roomStatus.ThermMeasuredTemperature,
					SetpointTemperature: roomStatus.ThermSetpointTemperature,
					SetpointMode:        roomStatus.ThermSetpointMode,
					HeatingPowerRequest: roomStatus.HeatingPowerRequest,
					OpenWindow:          roomStatus.OpenWindow,
					Reachable:           roomStatus.Reachable,
				},
			}

			c.metricsBuffer.Add(ctx, bufferReading)
			readingsAdded++

			c.logger.Debug("added Netatmo reading to metrics buffer",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
				zap.String("room_name", roomName),
				zap.String("room_id", roomStatus.ID),
				zap.Float64("measured_temp", roomStatus.ThermMeasuredTemperature),
				zap.Float64("setpoint_temp", roomStatus.ThermSetpointTemperature),
				zap.String("mode", roomStatus.ThermSetpointMode),
				zap.Int("heating_power", roomStatus.HeatingPowerRequest),
			)
		}

		span.SetAttributes(attribute.Int("readings_added", readingsAdded))

		c.logger.Debug("added Netatmo readings to metrics buffer",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Int("readings_added", readingsAdded),
		)
	}()
}

// runControlLoop executes one iteration of the control loop
// Uses home status received from Metric Job via channel
func (c *Controller) runControlLoop(ctx context.Context) {
	// Create a new root trace span for this job execution
	ctx, span := c.tracer.Start(ctx, "control_job",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("job", "control_job"),
			attribute.String("operation", "evaluate_and_control_rooms"),
			attribute.Int("mapping_count", len(c.config.Mappings)),
			attribute.Bool("dry_run", c.config.DryRun),
		),
	)
	defer span.End()

	c.logger.Info("control job started - waiting for home status from metric job",
		zap.Int("mapping_count", len(c.config.Mappings)),
		zap.Bool("dry_run", c.config.DryRun),
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
	)

	// Check if mappings configured
	if len(c.config.Mappings) == 0 {
		c.logger.Warn("control job - no thermostat mappings configured, skipping")
		span.SetAttributes(attribute.String("skip_reason", "no_mappings"))
		return
	}

	// Get home status from shared state (set by Metric Job)
	const maxDataAge = 30 * time.Second // If data older than 30s, fetch fresh
	var homeStatus *netatmo.HomeStatusResponse
	var metricJobTraceID string

	// Try to get data from shared state first
	sharedStatus, age, traceID, hasData := c.sharedHomeStatus.Get()

	if hasData && age <= maxDataAge {
		// Data is fresh, use it
		homeStatus = sharedStatus
		metricJobTraceID = traceID

		c.logger.Info("control job - using fresh data from metric job",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("metric_job_trace_id", metricJobTraceID),
			zap.Duration("data_age", age),
			zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		)

		span.SetAttributes(
			attribute.String("data_source", "metric_job"),
			attribute.String("metric_job_trace_id", metricJobTraceID),
			attribute.Int64("data_age_ms", age.Milliseconds()),
		)
	} else {
		// Data is stale or missing, fetch fresh data ourselves
		if hasData {
			c.logger.Warn("control job - shared data is stale, fetching fresh data from Netatmo API",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
				zap.Duration("data_age", age),
				zap.Duration("max_age", maxDataAge),
			)
		} else {
			c.logger.Warn("control job - no shared data available, fetching fresh data from Netatmo API",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
			)
		}

		fetchStart := time.Now()
		fetchedStatus, err := c.netatmoClient.GetHomeStatus(ctx, c.homeID)
		if err != nil {
			c.logger.Error("control job - failed to fetch home status from Netatmo API",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
				zap.Error(err),
			)
			span.RecordError(err)
			span.SetAttributes(attribute.String("skip_reason", "fetch_failed"))
			return
		}

		homeStatus = fetchedStatus
		fetchDuration := time.Since(fetchStart)

		c.logger.Info("control job - fetched fresh data from Netatmo API",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Duration("fetch_duration", fetchDuration),
			zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		)

		span.SetAttributes(
			attribute.String("data_source", "control_job_fetch"),
			attribute.Int64("fetch_duration_ms", fetchDuration.Milliseconds()),
		)
	}

	span.SetAttributes(
		attribute.String("home_id", c.homeID),
		attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)

	// Build room status map for quick lookup
	roomStatusMap := make(map[string]*netatmo.RoomStatus)
	for i := range homeStatus.Body.Home.Rooms {
		room := &homeStatus.Body.Home.Rooms[i]
		roomStatusMap[room.ID] = room
	}

	c.logger.Info("control job - processing rooms concurrently with per-room waiting logic",
		zap.Int("rooms_to_evaluate", len(c.config.Mappings)),
		zap.String("trace_id", span.SpanContext().TraceID().String()),
	)

	// Process rooms concurrently with per-room waiting for manual mode expiration
	skipCount, adjustCount, noAdjustCount := c.processRoomsConcurrently(ctx, roomStatusMap)

	// Record summary attributes on span
	span.SetAttributes(
		attribute.Int("rooms_evaluated", len(c.config.Mappings)),
		attribute.Int("skipped", skipCount),
		attribute.Int("adjusted", adjustCount),
		attribute.Int("no_adjustment", noAdjustCount),
	)

	c.logger.Info("control job completed - all rooms processed",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_evaluated", len(c.config.Mappings)),
		zap.Int("skipped", skipCount),
		zap.Int("adjusted", adjustCount),
		zap.Int("no_adjustment", noAdjustCount),
	)
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
	var mu sync.Mutex

	c.logger.Debug("spawning concurrent goroutines for room processing",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("goroutine_count", len(c.config.Mappings)),
	)

	for _, mapping := range c.config.Mappings {
		wg.Add(1)

		go func(m config.ThermostatMapping) {
			defer wg.Done()

			// Get room status from initial fetch
			roomStatus, roomExists := initialRoomStatusMap[m.RoomID]
			if !roomExists {
				c.logger.Warn("room processing - room not found in home status",
					zap.String("room_name", m.RoomName),
					zap.String("room_id", m.RoomID),
				)
				mu.Lock()
				skipCount++
				mu.Unlock()
				return
			}

			c.logger.Debug("room processing started",
				zap.String("room_name", m.RoomName),
				zap.String("mode", roomStatus.ThermSetpointMode),
				zap.Float64("current_setpoint", roomStatus.ThermSetpointTemperature),
				zap.Float64("measured_temp", roomStatus.ThermMeasuredTemperature),
			)

			// Check if room is in manual mode with algorithm-set override expiring within window
			if roomStatus.ThermSetpointMode == "manual" && c.shouldWaitForOverrideExpiration(roomStatus) {
				expirationTime := time.Unix(roomStatus.ThermSetpointEndTime, 0)
				waitDuration := time.Until(expirationTime.Add(1 * time.Second))

				c.logger.Info("room processing - waiting for algorithm-set override to expire",
					zap.String("room_name", m.RoomName),
					zap.Time("override_end_time", expirationTime),
					zap.Duration("wait_duration", waitDuration),
				)

				// Wait for override to expire
				if waitDuration > 0 {
					select {
					case <-time.After(waitDuration):
						// Override expired, fetch fresh status
						c.logger.Info("room processing - override expired, fetching fresh home status",
							zap.String("room_name", m.RoomName),
						)
						freshStatus, err := c.netatmoClient.GetHomeStatus(ctx, c.homeID)
						if err != nil {
							c.logger.Error("room processing - failed to fetch fresh home status after override expiration",
								zap.String("room_name", m.RoomName),
								zap.Error(err),
							)
							mu.Lock()
							skipCount++
							mu.Unlock()
							return
						}

						// Find this room in fresh status
						for i := range freshStatus.Body.Home.Rooms {
							if freshStatus.Body.Home.Rooms[i].ID == m.RoomID {
								roomStatus = &freshStatus.Body.Home.Rooms[i]
								c.logger.Debug("room processing - updated to fresh room status",
									zap.String("room_name", m.RoomName),
									zap.String("new_mode", roomStatus.ThermSetpointMode),
									zap.Float64("new_setpoint", roomStatus.ThermSetpointTemperature),
								)
								break
							}
						}
					case <-ctx.Done():
						c.logger.Warn("room processing - context cancelled while waiting for override expiration",
							zap.String("room_name", m.RoomName),
						)
						mu.Lock()
						skipCount++
						mu.Unlock()
						return
					}
				}
			}

			// Evaluate room with current or fresh status
			decision := c.evaluateRoom(ctx, m, map[string]*netatmo.RoomStatus{m.RoomID: roomStatus})

			c.logger.Debug("room processing - evaluation completed",
				zap.String("room_name", m.RoomName),
				zap.String("action", decision.Action),
				zap.String("reason", decision.Reason),
			)

			// Track decision type
			mu.Lock()
			switch decision.Action {
			case "skip":
				skipCount++
			case "set_manual_override":
				adjustCount++
			case "no_adjustment_needed":
				noAdjustCount++
			}
			mu.Unlock()

			// Push metrics and execute decision
			hardOverrideActive := c.isHardOverrideActive(m.RoomName)
			c.pushControlMetrics(ctx, decision, hardOverrideActive, false)
			c.executeDecision(ctx, decision)

			c.logger.Debug("room processing completed",
				zap.String("room_name", m.RoomName),
				zap.String("final_action", decision.Action),
			)
		}(mapping)
	}

	c.logger.Debug("waiting for all room processing goroutines to complete",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
	)

	wg.Wait()

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
