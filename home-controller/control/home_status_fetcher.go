package control

import (
	"context"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
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

	// Store home status in shared state for Control Job first
	// Includes trace ID for correlation between Metric Job and Control Job
	traceID := span.SpanContext().TraceID().String()
	m.controller.sharedHomeStatus.Set(homeStatus, traceID)

	span.SetAttributes(attribute.Bool("stored_to_shared_state", true))

	m.logger.Debug("metric job - stored home status in shared state",
		zap.String("trace_id", traceID),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)

	// Add Netatmo data to metrics buffer for Prometheus push
	m.logger.Debug("metric job - adding Netatmo data to metrics buffer",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)

	m.controller.addToMetricsBuffer(ctx, homeStatus)

	// Calculate and push Xiaomi weighted averages to metrics buffer
	m.logger.Debug("metric job - calculating and pushing Xiaomi weighted averages",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("mapping_count", len(m.controller.config.Mappings)),
	)

	xiaomiPushedCount := m.calculateAndPushXiaomiAverages(ctx)

	span.SetAttributes(attribute.Int("xiaomi_averages_pushed", xiaomiPushedCount))

	m.logger.Debug("metric job - Xiaomi weighted averages pushed",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("count", xiaomiPushedCount),
	)

	totalDuration := time.Since(fetchStart)
	span.SetAttributes(attribute.Int64("total_duration_ms", totalDuration.Milliseconds()))

	m.logger.Info("metric job completed - home status stored, Netatmo metrics and Xiaomi averages pushed",
		zap.String("trace_id", traceID),
		zap.Duration("total_duration", totalDuration),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		zap.Int("xiaomi_averages", xiaomiPushedCount),
	)
}

// calculateAndPushXiaomiAverages calculates weighted averages for all configured Xiaomi sensors
// and pushes them to metrics buffer for Prometheus export
func (m *MetricJob) calculateAndPushXiaomiAverages(ctx context.Context) int {
	ctx, span := m.tracer.Start(ctx, "calculate_and_push_xiaomi_averages",
		trace.WithAttributes(
			attribute.Int("mapping_count", len(m.controller.config.Mappings)),
		),
	)
	defer span.End()

	timestamp := time.Now()
	pushedCount := 0

	// Process each room mapping to calculate weighted average for its sensor
	processedSensors := make(map[string]bool) // Track which sensors we've already processed

	for _, mapping := range m.controller.config.Mappings {
		sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))

		// Skip if we've already processed this sensor (same sensor might be used in multiple rooms)
		if processedSensors[sensorMAC] {
			continue
		}
		processedSensors[sensorMAC] = true

		// Calculate weighted average temperature using controller's method
		xiaomiTemp, err := m.controller.getWeightedAverageTemperature(ctx, sensorMAC)
		if err != nil {
			m.logger.Debug("skipping Xiaomi average for sensor (no data available)",
				zap.String("sensor_mac", sensorMAC),
				zap.String("room_name", mapping.RoomName),
				zap.Error(err),
			)
			continue
		}

		// Get reading count from control buffer for this sensor
		now := time.Now()
		cutoff := now.Add(-60 * time.Second)
		readings := m.controller.controlBuffer.GetReadingsByTimeWindow(ctx, cutoff, now)
		readingCount := 0
		for _, reading := range readings {
			if reading.Type == buffer.ReadingTypeBLE && reading.BLE != nil {
				if strings.ToUpper(reading.BLE.MAC) == sensorMAC {
					readingCount++
				}
			}
		}

		// Create weighted average reading
		weightedAvgReading := &buffer.Reading{
			Type: buffer.ReadingTypeBLEWeightedAvg,
			WeightedAvg: &buffer.WeightedAvgReading{
				Timestamp:          timestamp,
				MAC:                sensorMAC,
				RoomName:           mapping.RoomName,
				SensorID:           0, // We don't have sensor ID in mapping, set to 0
				TemperatureCelsius: xiaomiTemp,
				ReadingCount:       readingCount,
			},
		}

		m.controller.metricsBuffer.Add(ctx, weightedAvgReading)
		pushedCount++

		m.logger.Debug("pushed Xiaomi weighted average to metrics buffer",
			zap.String("sensor_mac", sensorMAC),
			zap.String("room_name", mapping.RoomName),
			zap.Float64("weighted_avg", xiaomiTemp),
			zap.Int("reading_count", readingCount),
		)
	}

	span.SetAttributes(attribute.Int("pushed_count", pushedCount))

	m.logger.Debug("finished calculating and pushing Xiaomi weighted averages",
		zap.Int("total_pushed", pushedCount),
	)

	return pushedCount
}

// Deprecated: HomeStatusFetcher - Use MetricJob instead
type HomeStatusFetcher = MetricJob
