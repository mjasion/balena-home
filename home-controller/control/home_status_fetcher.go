package control

import (
	"context"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// MetricJob handles fetching home status and storing to shared state
type MetricJob struct {
	controller *Controller
	logger     *zap.Logger
	tracer     trace.Tracer
}

// NewMetricJob creates a new metric job
func NewMetricJob(controller *Controller, logger *zap.Logger, tracer trace.Tracer) *MetricJob {
	return &MetricJob{
		controller: controller,
		logger:     logger,
		tracer:     tracer,
	}
}

// Run executes the metric job (called by scheduler every minute)
func (m *MetricJob) Run(ctx context.Context) {
	// Create a new root trace span for this job execution
	ctx, span := m.tracer.Start(ctx, "metric_job",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("home_id", m.controller.homeID),
			attribute.String("job", "metric_job"),
			attribute.String("operation", "fetch_home_status"),
		),
	)
	defer span.End()

	fetchStart := time.Now()

	m.logger.Info("metric job started - fetching home status from Netatmo API",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
		zap.String("home_id", m.controller.homeID),
	)

	// Fetch home status from Netatmo
	homeStatus, err := m.controller.netatmoClient.GetHomeStatus(ctx, m.controller.homeID)

	if err != nil {
		m.logger.Error("metric job failed - could not fetch home status from Netatmo API",
			zap.Error(err),
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("home_id", m.controller.homeID),
		)
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("fetch_success", false))
		return
	}

	fetchDuration := time.Since(fetchStart)

	span.SetAttributes(
		attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		attribute.Bool("fetch_success", true),
		attribute.Int64("fetch_duration_ms", fetchDuration.Milliseconds()),
	)

	m.logger.Info("metric job - home status fetched successfully from Netatmo API",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		zap.Duration("fetch_duration", fetchDuration),
	)

	// Add Netatmo data to metrics buffer for Prometheus push
	m.logger.Debug("metric job - adding Netatmo data to metrics buffer",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)
	m.controller.addToMetricsBuffer(ctx, homeStatus)

	// Push temperature difference metrics (BLE vs Netatmo) for 1-minute resolution
	m.pushTemperatureDifferenceMetrics(ctx, homeStatus)

	// Store home status in shared state for Control Job
	// Includes trace ID for correlation between Metric Job and Control Job
	traceID := span.SpanContext().TraceID().String()
	m.controller.sharedHomeStatus.Set(homeStatus, traceID)

	totalDuration := time.Since(fetchStart)
	span.SetAttributes(
		attribute.Bool("stored_to_shared_state", true),
		attribute.Int64("total_duration_ms", totalDuration.Milliseconds()),
	)

	m.logger.Info("metric job completed - home status stored in shared state",
		zap.String("trace_id", traceID),
		zap.Duration("total_duration", totalDuration),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)
}

// pushTemperatureDifferenceMetrics calculates and pushes temperature difference metrics
// for each room-to-sensor mapping, giving 1-minute resolution instead of 15-minute from the Control Job.
func (m *MetricJob) pushTemperatureDifferenceMetrics(ctx context.Context, homeStatus *netatmo.HomeStatusResponse) {
	_, span := m.tracer.Start(ctx, "push_temperature_difference_metrics",
		trace.WithAttributes(
			attribute.Int("mapping_count", len(m.controller.config.Mappings)),
		),
	)
	defer span.End()

	// Build room status map for quick lookup
	roomStatusMap := make(map[string]*netatmo.RoomStatus)
	for i := range homeStatus.Body.Home.Rooms {
		room := &homeStatus.Body.Home.Rooms[i]
		roomStatusMap[room.ID] = room
	}

	pushedCount := 0
	for _, mapping := range m.controller.config.Mappings {
		roomStatus, roomExists := roomStatusMap[mapping.RoomID]
		if !roomExists {
			m.logger.Debug("metric job - room not found in home status, skipping temperature difference",
				zap.String("room_name", mapping.RoomName),
				zap.String("room_id", mapping.RoomID),
			)
			continue
		}

		sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
		xiaomiTemp, err := m.controller.getWeightedAverageTemperature(ctx, sensorMAC)
		if err != nil || xiaomiTemp == 0 {
			m.logger.Debug("metric job - BLE sensor data unavailable, skipping temperature difference",
				zap.String("room_name", mapping.RoomName),
				zap.String("sensor_mac", sensorMAC),
				zap.Error(err),
			)
			continue
		}

		scheduledTemp := m.controller.determineScheduledTemp(mapping.RoomName, roomStatus)
		tempDiff := xiaomiTemp - roomStatus.ThermMeasuredTemperature
		hardOverrideActive := m.controller.isHardOverrideActive(mapping.RoomName)

		reading := &buffer.Reading{
			Type: buffer.ReadingTypeControl,
			Control: &buffer.ControlReading{
				Timestamp:             time.Now(),
				RoomName:              mapping.RoomName,
				Action:                "metric",
				XiaomiTemperature:     xiaomiTemp,
				ScheduledTemperature:  scheduledTemp,
				ThermostatMeasured:    roomStatus.ThermMeasuredTemperature,
				ThermostatMode:        roomStatus.ThermSetpointMode,
				CalculatedSetpoint:    0,
				TemperatureDifference: tempDiff,
				SetpointAdjustment:    0,
				ExternallyModified:    false,
				HardOverrideActive:    hardOverrideActive,
			},
		}

		m.controller.metricsBuffer.Add(ctx, reading)
		pushedCount++

		m.logger.Debug("metric job - pushed temperature difference metric",
			zap.String("room_name", mapping.RoomName),
			zap.Float64("xiaomi_temp", xiaomiTemp),
			zap.Float64("thermostat_measured", roomStatus.ThermMeasuredTemperature),
			zap.Float64("scheduled_temp", scheduledTemp),
			zap.Float64("temp_diff", tempDiff),
			zap.String("mode", roomStatus.ThermSetpointMode),
		)
	}

	span.SetAttributes(attribute.Int("metrics_pushed", pushedCount))

	m.logger.Debug("metric job - temperature difference metrics pushed",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("metrics_pushed", pushedCount),
	)
}

// Deprecated: HomeStatusFetcher - Use MetricJob instead
type HomeStatusFetcher = MetricJob
