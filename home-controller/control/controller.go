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

	// Schedule sync tracking
	lastSyncTime time.Time
	syncMu       sync.RWMutex
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

// Initialize initializes the controller state (must be called before Run)
func (c *Controller) Initialize(ctx context.Context) error {
	c.logger.Info("initializing thermostat controller",
		zap.Int("mapping_count", len(c.config.Mappings)),
		zap.String("cron", c.config.Cron),
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

	// Check if mappings configured
	if len(c.config.Mappings) == 0 {
		c.logger.Warn("no thermostat mappings configured, skipping control loop")
		return
	}

	homeID := c.homeID

	// Determine if schedule sync is needed
	needsSync := c.shouldSyncSchedule()

	span.SetAttributes(
		attribute.String("home_id", homeID),
		attribute.Bool("needs_sync", needsSync),
	)

	// Execute appropriate mode (sync or normal)
	var skipCount, adjustCount, noAdjustCount int
	if needsSync {
		skipCount, adjustCount, noAdjustCount = c.runSyncMode(ctx, homeID)
	} else {
		skipCount, adjustCount, noAdjustCount = c.runNormalMode(ctx, homeID)
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
