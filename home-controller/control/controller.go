package control

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"github.com/mjasion/balena-home/thermostats/scheduler"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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

	// State tracking (thread-safe with mutex)
	stateMu     sync.RWMutex
	stateByRoom map[string]*ThermostatState // Key: RoomID

	// Mapping of sensor MAC to room IDs
	sensorToRooms map[string][]string // Key: sensor MAC (uppercase), Value: list of room IDs
}

// New creates a new thermostat controller
func New(
	cfg *config.ThermostatControlConfig,
	netatmoClient *netatmo.Client,
	controlBuffer *buffer.RingBuffer,
	metricsBuffer *buffer.RingBuffer,
	logger *zap.Logger,
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
		config:        cfg,
		netatmoClient: netatmoClient,
		controlBuffer: controlBuffer,
		metricsBuffer: metricsBuffer,
		logger:        logger,
		tracer:        tracer,
		stateByRoom:   make(map[string]*ThermostatState),
		sensorToRooms: sensorToRooms,
	}
}

// Start begins the control loop
func (c *Controller) Start(ctx context.Context) error {
	c.logger.Info("starting thermostat control loop",
		zap.Int("control_interval_seconds", c.config.ControlIntervalSeconds),
		zap.Int("mapping_count", len(c.config.Mappings)),
	)

	// Initialize state from Netatmo API (get room IDs)
	if err := c.initializeRoomIDs(ctx); err != nil {
		return fmt.Errorf("failed to initialize room IDs: %w", err)
	}

	// Calculate time until next aligned interval and wait
	intervalDuration := time.Duration(c.config.ControlIntervalSeconds) * time.Second
	initialWait := scheduler.WaitForAlignedInterval(intervalDuration, c.logger)

	// Wait for first aligned interval
	select {
	case <-ctx.Done():
		c.logger.Info("thermostat control loop stopped before first run")
		return nil
	case <-time.After(initialWait):
		// Run control loop at aligned time
		c.runControlLoop(ctx)
	}

	// Now use ticker for subsequent runs
	ticker := time.NewTicker(intervalDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("thermostat control loop stopped")
			return nil
		case <-ticker.C:
			c.runControlLoop(ctx)
		}
	}
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

// runControlLoop executes one iteration of the control loop
func (c *Controller) runControlLoop(ctx context.Context) {
	// Start a new trace span for this control loop iteration
	ctx, span := c.tracer.Start(ctx, "control_loop_iteration",
		trace.WithAttributes(
			attribute.Int("mapping_count", len(c.config.Mappings)),
			attribute.Bool("dry_run", c.config.DryRun),
		),
	)
	defer span.End()

	c.logger.Debug("control loop iteration started",
		zap.Int("mapping_count", len(c.config.Mappings)),
		zap.Bool("dry_run", c.config.DryRun),
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
	)

	// Fetch current Netatmo home status
	// Get home ID from first mapping (all mappings are in the same home)
	if len(c.config.Mappings) == 0 {
		c.logger.Warn("no thermostat mappings configured, skipping control loop")
		return
	}

	// Get home ID from Netatmo (use cached from initialization)
	var homesData *netatmo.HomesDataResponse
	var homeID string
	var err error
	{
		_, fetchHomesSpan := c.tracer.Start(ctx, "fetch_netatmo_homes")
		homesData, err = c.netatmoClient.GetHomesData(ctx)
		if err != nil {
			fetchHomesSpan.RecordError(err)
			fetchHomesSpan.SetStatus(codes.Error, "failed to fetch homes data")
			fetchHomesSpan.End()
			c.logger.Error("failed to fetch homes data", zap.Error(err))
			span.SetStatus(codes.Error, "failed to fetch homes data")
			return
		}
		fetchHomesSpan.SetAttributes(attribute.Int("homes_count", len(homesData.Body.Homes)))
		fetchHomesSpan.End()

		if len(homesData.Body.Homes) == 0 {
			c.logger.Error("no homes found in Netatmo account")
			span.SetStatus(codes.Error, "no homes found")
			return
		}

		homeID = homesData.Body.Homes[0].ID
		span.SetAttributes(attribute.String("home_id", homeID))
	}

	// Fetch home status
	var homeStatus *netatmo.HomeStatusResponse
	{
		_, fetchStatusSpan := c.tracer.Start(ctx, "fetch_netatmo_home_status",
			trace.WithAttributes(attribute.String("home_id", homeID)),
		)
		homeStatus, err = c.netatmoClient.GetHomeStatus(ctx, homeID)
		if err != nil {
			fetchStatusSpan.RecordError(err)
			fetchStatusSpan.SetStatus(codes.Error, "failed to fetch home status")
			fetchStatusSpan.End()
			c.logger.Error("failed to fetch home status", zap.Error(err))
			span.SetStatus(codes.Error, "failed to fetch home status")
			return
		}
		fetchStatusSpan.SetAttributes(attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)))
		fetchStatusSpan.End()
	}

	// Build map of room ID to room status
	roomStatusMap := make(map[string]*netatmo.RoomStatus)
	for i := range homeStatus.Body.Home.Rooms {
		room := &homeStatus.Body.Home.Rooms[i]
		roomStatusMap[room.ID] = room
	}

	// Process each mapping
	skipCount := 0
	adjustCount := 0
	noAdjustCount := 0

	for _, mapping := range c.config.Mappings {
		decision := c.evaluateRoom(ctx, mapping, roomStatusMap)

		// Track decision types
		switch decision.Action {
		case "skip":
			skipCount++
		case "set_manual_override":
			adjustCount++
		case "no_adjustment_needed":
			noAdjustCount++
		}

		// Push metrics for this decision
		c.stateMu.RLock()
		state := c.stateByRoom[mapping.RoomID]
		externallyModified := false
		if state != nil {
			externallyModified = state.ExternallyModified
		}
		c.stateMu.RUnlock()

		// Check if hard override is active
		hardOverrideActive := false
		for _, override := range c.config.HardOverrides {
			if override.RoomName == mapping.RoomName {
				_, active := c.getHardOverrideTemp(override)
				if active {
					hardOverrideActive = true
					break
				}
			}
		}

		c.pushControlMetrics(decision, hardOverrideActive, externallyModified)
		c.executeDecision(ctx, homeID, decision)
	}

	// Record summary attributes on span
	span.SetAttributes(
		attribute.Int("rooms_evaluated", len(c.config.Mappings)),
		attribute.Int("skipped", skipCount),
		attribute.Int("adjusted", adjustCount),
		attribute.Int("no_adjustment", noAdjustCount),
	)

	c.logger.Info("control loop iteration completed",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_evaluated", len(c.config.Mappings)),
		zap.Int("skipped", skipCount),
		zap.Int("adjusted", adjustCount),
		zap.Int("no_adjustment", noAdjustCount),
	)
}

// evaluateRoom evaluates whether a room needs thermostat adjustment
func (c *Controller) evaluateRoom(
	ctx context.Context,
	mapping config.ThermostatMapping,
	roomStatusMap map[string]*netatmo.RoomStatus,
) ControlDecision {
	ctx, span := c.tracer.Start(ctx, "evaluate_room",
		trace.WithAttributes(
			attribute.String("room_name", mapping.RoomName),
			attribute.String("room_id", mapping.RoomID),
			attribute.String("sensor_mac", mapping.SensorMAC),
		),
	)

	decision := ControlDecision{
		RoomID:   mapping.RoomID,
		RoomName: mapping.RoomName,
		Action:   "skip",
	}

	defer func() {
		// Record decision attributes before span ends
		span.SetAttributes(
			attribute.String("decision_action", decision.Action),
			attribute.String("decision_reason", decision.Reason),
			attribute.Float64("xiaomi_temperature", decision.XiaomiTemperature),
			attribute.Float64("scheduled_temperature", decision.ScheduledTemp),
			attribute.Float64("thermostat_measured", decision.ThermostatMeasured),
		)
		if decision.Action == "set_manual_override" {
			span.SetAttributes(attribute.Float64("calculated_setpoint", decision.CalculatedSetpoint))
		}
		span.End()
	}()

	// Get current state (defensive copy)
	c.stateMu.RLock()
	state, exists := c.stateByRoom[mapping.RoomID]
	if !exists {
		c.stateMu.RUnlock()
		decision.Reason = "room state not initialized"
		return decision
	}
	stateCopy := state.Copy()
	c.stateMu.RUnlock()

	// Get room status early to populate temperature fields in all cases
	roomStatus, roomExists := roomStatusMap[mapping.RoomID]
	if roomExists && roomStatus.Reachable {
		// Populate thermostat measured temperature
		decision.ThermostatMeasured = roomStatus.ThermMeasuredTemperature

		// Calculate scheduled temperature (including hard overrides)
		scheduledTemp := roomStatus.ThermSetpointTemperature
		for _, override := range c.config.HardOverrides {
			if override.RoomName == mapping.RoomName {
				targetTemp, active := c.getHardOverrideTemp(override)
				if active {
					scheduledTemp = targetTemp
					break
				}
			}
		}
		decision.ScheduledTemp = scheduledTemp

		// Get Xiaomi sensor readings (last 60 seconds, weighted average)
		sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
		xiaomiTemp, err := c.getWeightedAverageTemperature(sensorMAC)
		if err == nil {
			decision.XiaomiTemperature = xiaomiTemp
		}
		// If sensor data unavailable, XiaomiTemperature remains 0 (logged but not critical for skip decisions)
	}

	// Check precedence hierarchy
	// 1. External modification detected - skip control entirely
	if stateCopy.ExternallyModified {
		// Check reset conditions
		if roomExists {
			// Reset ONLY if mode is "schedule" (user explicitly returned to schedule mode)
			if roomStatus.ThermSetpointMode == "schedule" {
				c.logger.Info("external modification cleared: thermostat returned to schedule mode",
					zap.String("room_name", mapping.RoomName),
				)
				c.clearExternalModification(mapping.RoomID)
			} else {
				// Stay paused - respect manual override indefinitely
				decision.Reason = "externally modified, respecting manual override"
				return decision
			}
		} else {
			decision.Reason = "externally modified, room status unavailable"
			return decision
		}
	}

	// Check if we're in recheck delay period
	if time.Now().Before(stateCopy.NextRecheckTime) {
		decision.Reason = fmt.Sprintf("waiting until next recheck time (%s)",
			stateCopy.NextRecheckTime.Format("15:04:05"))
		return decision
	}

	// Validate room status is available and reachable for control decisions
	if !roomExists {
		decision.Reason = "room not found in Netatmo status"
		c.logger.Warn("room not found in Netatmo home status",
			zap.String("room_name", mapping.RoomName),
			zap.String("room_id", mapping.RoomID),
		)
		return decision
	}

	if !roomStatus.Reachable {
		decision.Reason = "thermostat not reachable"
		return decision
	}

	// Validate Xiaomi sensor data is available for control decisions
	if decision.XiaomiTemperature == 0 {
		sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
		decision.Reason = "sensor data unavailable"
		c.logger.Warn("sensor data unavailable for control",
			zap.String("room_name", mapping.RoomName),
			zap.String("sensor_mac", sensorMAC),
		)
		return decision
	}

	// 2. Extract values for control logic (already populated in decision)
	xiaomiTemp := decision.XiaomiTemperature
	scheduledTemp := decision.ScheduledTemp
	hardOverrideActive := false
	for _, override := range c.config.HardOverrides {
		if override.RoomName == mapping.RoomName {
			_, active := c.getHardOverrideTemp(override)
			if active {
				hardOverrideActive = true
				c.logger.Debug("hard override active",
					zap.String("room_name", mapping.RoomName),
					zap.Float64("override_temp", scheduledTemp),
				)
				break
			}
		}
	}

	// 3. Calculate temperature difference
	tempDiff := xiaomiTemp - scheduledTemp
	if math.Abs(tempDiff) < c.config.TemperatureThreshold {
		decision.Action = "no_adjustment_needed"
		decision.Reason = fmt.Sprintf("temperature difference %.2f°C below threshold %.2f°C",
			math.Abs(tempDiff), c.config.TemperatureThreshold)
		return decision
	}

	// 4. Calculate setpoint to compensate for sensor offset
	// The Netatmo uses its own built-in sensor for control, which may differ from the Xiaomi sensor.
	// We need to set a setpoint that ensures the Netatmo will heat/cool appropriately.
	//
	// Strategy:
	// - If room is too cold (xiaomi < scheduled): set setpoint ABOVE thermostatMeasured + 0.5°C
	// - If room is too warm (xiaomi > scheduled): set setpoint BELOW thermostatMeasured - 0.5°C
	// - The 0.5°C offset ensures the thermostat actually triggers heating/cooling
	var calculatedSetpoint float64

	if tempDiff < 0 {
		// Room too cold - need to heat
		// Ensure setpoint is high enough to trigger heating on Netatmo's sensor
		calculatedSetpoint = math.Max(scheduledTemp, decision.ThermostatMeasured+0.5)
	} else {
		// Room too warm - need to cool (or stop heating)
		// Ensure setpoint is low enough to stop heating on Netatmo's sensor
		calculatedSetpoint = math.Min(scheduledTemp, decision.ThermostatMeasured-0.5)
	}

	// Check if setpoint matches schedule (within 0.1°C tolerance)
	// If so, no need for manual override
	if math.Abs(calculatedSetpoint-scheduledTemp) < 0.1 {
		decision.Action = "no_adjustment_needed"
		decision.Reason = fmt.Sprintf("calculated setpoint (%.1f°C) matches schedule, no override needed", calculatedSetpoint)
		return decision
	}

	// Check if thermostat is already heating/cooling correctly despite sensor offset
	// This avoids frost-guard errors when sensors disagree significantly
	if tempDiff > 0 {
		// Xiaomi says room is too warm, wants to cool down
		// But if thermostat sensor shows it's already below target, heating is working correctly
		if decision.ThermostatMeasured < scheduledTemp {
			decision.Action = "no_adjustment_needed"
			decision.Reason = "sensor offset detected, heating working correctly"
			c.logger.Info("skipping override due to sensor offset",
				zap.String("room_name", mapping.RoomName),
				zap.Float64("xiaomi_temp", xiaomiTemp),
				zap.Float64("thermostat_measured", decision.ThermostatMeasured),
				zap.Float64("scheduled_temp", scheduledTemp),
				zap.Float64("temp_diff", tempDiff),
				zap.String("condition", "xiaomi shows warm but thermostat already below target"),
			)
			return decision
		}
	} else {
		// Xiaomi says room is too cold, wants to heat
		// But if thermostat sensor shows it's already above target, we don't need more heating
		if decision.ThermostatMeasured > scheduledTemp {
			decision.Action = "no_adjustment_needed"
			decision.Reason = "sensor offset detected, no heating needed"
			c.logger.Info("skipping override due to sensor offset",
				zap.String("room_name", mapping.RoomName),
				zap.Float64("xiaomi_temp", xiaomiTemp),
				zap.Float64("thermostat_measured", decision.ThermostatMeasured),
				zap.Float64("scheduled_temp", scheduledTemp),
				zap.Float64("temp_diff", tempDiff),
				zap.String("condition", "xiaomi shows cold but thermostat already above target"),
			)
			return decision
		}
	}

	// Safety bounds: 7.0-30.0°C (Netatmo API limits)
	if calculatedSetpoint < 7.0 {
		calculatedSetpoint = 7.0
	} else if calculatedSetpoint > 30.0 {
		calculatedSetpoint = 30.0
	}

	decision.Action = "set_manual_override"
	decision.CalculatedSetpoint = calculatedSetpoint
	decision.OverrideEndTime = time.Now().Add(time.Duration(c.config.OverrideDurationMinutes) * time.Minute).Unix()
	decision.Reason = fmt.Sprintf("xiaomi=%.1f°C, target=%.1f°C, diff=%.2f°C%s",
		xiaomiTemp, scheduledTemp, tempDiff,
		map[bool]string{true: " (hard override)", false: ""}[hardOverrideActive])

	// Detect external modification (if we previously sent a command)
	if !stateCopy.LastSetpointTime.IsZero() &&
		time.Since(stateCopy.LastSetpointTime) > 2*time.Minute {
		// Check if current setpoint differs from what we sent
		delta := roomStatus.ThermSetpointTemperature - stateCopy.LastSetpoint
		if math.Abs(delta) > 0.1 {
			timeSinceLastCommand := time.Since(stateCopy.LastSetpointTime)

			c.logger.Warn("external modification detected - backing off from automation indefinitely",
				zap.String("room_name", mapping.RoomName),
				zap.Float64("expected_setpoint", stateCopy.LastSetpoint),
				zap.Float64("actual_setpoint", roomStatus.ThermSetpointTemperature),
				zap.Float64("delta", delta),
				zap.Duration("time_since_last_command", timeSinceLastCommand),
				zap.Time("last_command_sent_at", stateCopy.LastSetpointTime),
				zap.String("resume_condition", "automation will resume only when thermostat is switched back to 'schedule' mode"),
				zap.String("reason", "thermostat was manually changed - respecting user preference indefinitely"),
			)
			c.markExternallyModified(mapping.RoomID)
			decision.Action = "skip"
			decision.Reason = "external modification detected"
			return decision
		}
	}

	return decision
}

// executeDecision executes the control decision
func (c *Controller) executeDecision(ctx context.Context, homeID string, decision ControlDecision) {
	ctx, span := c.tracer.Start(ctx, "execute_decision",
		trace.WithAttributes(
			attribute.String("room_name", decision.RoomName),
			attribute.String("room_id", decision.RoomID),
			attribute.String("action", decision.Action),
		),
	)
	defer span.End()

	if decision.Action == "skip" || decision.Action == "no_adjustment_needed" {
		if c.config.DryRun {
			c.logger.Info("[DRY-RUN] would NOT set temperature",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
				zap.String("room_name", decision.RoomName),
				zap.String("action", decision.Action),
				zap.String("reason", decision.Reason),
				zap.Float64("xiaomi_temp", decision.XiaomiTemperature),
				zap.Float64("scheduled_temp", decision.ScheduledTemp),
				zap.Float64("thermostat_measured", decision.ThermostatMeasured),
			)
		} else {
			c.logger.Info("control decision",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
				zap.String("room_name", decision.RoomName),
				zap.String("action", decision.Action),
				zap.String("reason", decision.Reason),
				zap.Float64("xiaomi_temp", decision.XiaomiTemperature),
				zap.Float64("scheduled_temp", decision.ScheduledTemp),
				zap.Float64("thermostat_measured", decision.ThermostatMeasured),
			)
		}
		return
	}

	if decision.Action == "set_manual_override" {
		// Apply safety limits
		safeSetpoint := decision.CalculatedSetpoint
		clamped := false

		if safeSetpoint < c.config.MinSetpointCelsius {
			c.logger.Warn("calculated setpoint below safety limit, clamping to minimum",
				zap.String("room_name", decision.RoomName),
				zap.Float64("calculated_setpoint", decision.CalculatedSetpoint),
				zap.Float64("min_setpoint", c.config.MinSetpointCelsius),
			)
			safeSetpoint = c.config.MinSetpointCelsius
			clamped = true
		} else if safeSetpoint > c.config.MaxSetpointCelsius {
			c.logger.Warn("calculated setpoint above safety limit, clamping to maximum",
				zap.String("room_name", decision.RoomName),
				zap.Float64("calculated_setpoint", decision.CalculatedSetpoint),
				zap.Float64("max_setpoint", c.config.MaxSetpointCelsius),
			)
			safeSetpoint = c.config.MaxSetpointCelsius
			clamped = true
		}

		// Dry-run mode: log what would be sent but don't call API
		if c.config.DryRun {
			c.logger.Info("[DRY-RUN] WOULD set temperature (not actually sent)",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
				zap.String("room_name", decision.RoomName),
				zap.String("room_id", decision.RoomID),
				zap.Float64("new_setpoint", safeSetpoint),
				zap.Float64("original_calculated_setpoint", decision.CalculatedSetpoint),
				zap.Bool("clamped", clamped),
				zap.Float64("xiaomi_temp", decision.XiaomiTemperature),
				zap.Float64("scheduled_temp", decision.ScheduledTemp),
				zap.Float64("thermostat_measured", decision.ThermostatMeasured),
				zap.String("reason", decision.Reason),
			)

			// Update state even in dry-run mode (for testing recheck delays)
			c.stateMu.Lock()
			if state, exists := c.stateByRoom[decision.RoomID]; exists {
				state.LastSetpoint = safeSetpoint
				state.LastSetpointTime = time.Now()
				state.NextRecheckTime = time.Now().Add(time.Duration(c.config.RecheckDelayMinutes) * time.Minute)
			}
			c.stateMu.Unlock()
			return
		}

		c.logger.Info("setting thermostat override",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", decision.RoomName),
			zap.Float64("new_setpoint", safeSetpoint),
			zap.Float64("original_calculated_setpoint", decision.CalculatedSetpoint),
			zap.Bool("clamped", clamped),
			zap.Float64("xiaomi_temp", decision.XiaomiTemperature),
			zap.Float64("scheduled_temp", decision.ScheduledTemp),
			zap.Float64("thermostat_measured", decision.ThermostatMeasured),
			zap.String("reason", decision.Reason),
		)

		// Call Netatmo API
		var err error
		{
			_, setTempSpan := c.tracer.Start(ctx, "set_netatmo_thermostat",
				trace.WithAttributes(
					attribute.String("home_id", homeID),
					attribute.String("room_id", decision.RoomID),
					attribute.Float64("setpoint", safeSetpoint),
					attribute.String("mode", "manual"),
				),
			)
			err = c.netatmoClient.SetRoomThermpoint(
				ctx,
				homeID,
				decision.RoomID,
				"manual",
				safeSetpoint,
				decision.OverrideEndTime,
			)
			if err != nil {
				setTempSpan.RecordError(err)
				setTempSpan.SetStatus(codes.Error, "failed to set thermostat setpoint")
			}
			setTempSpan.End()
		}

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to set thermostat setpoint")
			c.logger.Error("failed to set thermostat setpoint",
				zap.String("trace_id", span.SpanContext().TraceID().String()),
				zap.String("room_name", decision.RoomName),
				zap.Error(err),
			)
			return
		}

		// Update state
		c.stateMu.Lock()
		if state, exists := c.stateByRoom[decision.RoomID]; exists {
			state.LastSetpoint = safeSetpoint
			state.LastSetpointTime = time.Now()
			state.NextRecheckTime = time.Now().Add(time.Duration(c.config.RecheckDelayMinutes) * time.Minute)
		}
		c.stateMu.Unlock()

		c.logger.Info("thermostat override set successfully",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("room_name", decision.RoomName),
			zap.Float64("setpoint", safeSetpoint),
			zap.Time("next_recheck", time.Now().Add(time.Duration(c.config.RecheckDelayMinutes)*time.Minute)),
		)
	}
}

// getWeightedAverageTemperature calculates the weighted average temperature from sensor readings in the last 60 seconds
func (c *Controller) getWeightedAverageTemperature(sensorMAC string) (float64, error) {
	_, span := c.tracer.Start(context.Background(), "get_weighted_average_temperature",
		trace.WithAttributes(attribute.String("sensor_mac", sensorMAC)),
	)
	defer span.End()

	now := time.Now()
	cutoff := now.Add(-60 * time.Second)

	// Get readings from control buffer
	readings := c.controlBuffer.GetReadingsByTimeWindow(cutoff, now)

	if len(readings) == 0 {
		return 0, fmt.Errorf("no sensor readings in last 60 seconds")
	}

	// Filter readings for this sensor MAC
	var sensorReadings []SensorReading
	for _, reading := range readings {
		if reading.Type == buffer.ReadingTypeBLE && reading.BLE != nil {
			if strings.ToUpper(reading.BLE.MAC) == sensorMAC {
				// Type assert timestamp to time.Time
				if ts, ok := reading.BLE.Timestamp.(time.Time); ok {
					sensorReadings = append(sensorReadings, SensorReading{
						Timestamp:   ts,
						Temperature: reading.BLE.TemperatureCelsius,
					})
				}
			}
		}
	}

	if len(sensorReadings) == 0 {
		return 0, fmt.Errorf("no readings found for sensor %s in last 60 seconds", sensorMAC)
	}

	// Calculate weighted average
	// weight = (timestamp - cutoffTime) / (now - cutoffTime)
	var weightedSum float64
	var totalWeight float64
	timeRange := now.Sub(cutoff).Seconds()

	for _, reading := range sensorReadings {
		weight := reading.Timestamp.Sub(cutoff).Seconds() / timeRange
		weightedSum += reading.Temperature * weight
		totalWeight += weight
	}

	var weightedAvg float64
	if totalWeight == 0 {
		// Fallback: use simple average
		var sum float64
		for _, reading := range sensorReadings {
			sum += reading.Temperature
		}
		weightedAvg = sum / float64(len(sensorReadings))
	} else {
		weightedAvg = weightedSum / totalWeight
	}

	// Round to 2 decimal places
	weightedAvg = math.Round(weightedAvg*100) / 100

	// Record span attributes
	span.SetAttributes(
		attribute.Int("reading_count", len(sensorReadings)),
		attribute.Float64("weighted_average", weightedAvg),
		attribute.Int("time_window_seconds", 60),
	)

	c.logger.Debug("calculated weighted average temperature",
		zap.String("sensor_mac", sensorMAC),
		zap.Int("reading_count", len(sensorReadings)),
		zap.Float64("weighted_average", weightedAvg),
	)

	return weightedAvg, nil
}

// getHardOverrideTemp checks if a hard override is currently active and returns the target temperature
func (c *Controller) getHardOverrideTemp(override config.HardOverride) (float64, bool) {
	now := time.Now()
	currentTime := now.Format("15:04")
	currentDay := now.Weekday().String()[:3] // "Mon", "Tue", etc.

	for _, window := range override.Schedule {
		// Check if time matches
		if currentTime >= window.StartTime && currentTime <= window.EndTime {
			// If days are specified, check if current day matches
			if len(window.Days) > 0 {
				dayMatches := false
				for _, day := range window.Days {
					// Normalize to short form for comparison
					normalizedDay := day
					if len(day) > 3 {
						normalizedDay = day[:3]
					}
					if normalizedDay == currentDay {
						dayMatches = true
						break
					}
				}
				if !dayMatches {
					continue // Skip this window, day doesn't match
				}
			}
			// Time matches and (days match or no days specified)
			return window.TargetTemperature, true
		}
	}

	return 0, false
}

// markExternallyModified marks a thermostat as externally modified
func (c *Controller) markExternallyModified(roomID string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if state, exists := c.stateByRoom[roomID]; exists {
		state.ExternallyModified = true
		state.ExternalModificationTime = time.Now()
	}
}

// clearExternalModification clears the external modification flag
func (c *Controller) clearExternalModification(roomID string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if state, exists := c.stateByRoom[roomID]; exists {
		state.ExternallyModified = false
		state.ExternalModificationTime = time.Time{}
	}
}

// getMapKeys returns the keys of a map as a slice (helper function)
func getMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
