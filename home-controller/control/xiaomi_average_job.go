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

// XiaomiAverageJob handles calculating and pushing Xiaomi weighted averages to the metrics buffer
type XiaomiAverageJob struct {
	controller *Controller
	logger     *zap.Logger
	tracer     trace.Tracer
}

// NewXiaomiAverageJob creates a new Xiaomi average job
func NewXiaomiAverageJob(controller *Controller, logger *zap.Logger, tracer trace.Tracer) *XiaomiAverageJob {
	return &XiaomiAverageJob{
		controller: controller,
		logger:     logger,
		tracer:     tracer,
	}
}

// Run executes the Xiaomi average job (called by scheduler)
func (x *XiaomiAverageJob) Run(ctx context.Context) {
	// Create a new root trace span for this job execution
	ctx, span := x.tracer.Start(ctx, "xiaomi_average_job",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("job", "xiaomi_average_job"),
			attribute.String("operation", "calculate_and_push_xiaomi_averages"),
			attribute.Int("mapping_count", len(x.controller.config.Mappings)),
		),
	)
	defer span.End()

	x.logger.Info("xiaomi average job started - calculating and pushing Xiaomi weighted averages",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
		zap.Int("mapping_count", len(x.controller.config.Mappings)),
	)

	xiaomiPushedCount := x.calculateAndPushXiaomiAverages(ctx)

	span.SetAttributes(attribute.Int("xiaomi_averages_pushed", xiaomiPushedCount))

	x.logger.Info("xiaomi average job completed - Xiaomi weighted averages pushed",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("count", xiaomiPushedCount),
	)
}

// calculateAndPushXiaomiAverages calculates weighted averages for all configured Xiaomi sensors
// and pushes them to metrics buffer for Prometheus export
func (x *XiaomiAverageJob) calculateAndPushXiaomiAverages(ctx context.Context) int {
	ctx, span := x.tracer.Start(ctx, "calculate_and_push_xiaomi_averages",
		trace.WithAttributes(
			attribute.Int("mapping_count", len(x.controller.config.Mappings)),
		),
	)
	defer span.End()

	timestamp := time.Now()
	pushedCount := 0

	// Process each room mapping to calculate weighted average for its sensor
	processedSensors := make(map[string]bool) // Track which sensors we've already processed

	for _, mapping := range x.controller.config.Mappings {
		sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))

		// Skip if we've already processed this sensor (same sensor might be used in multiple rooms)
		if processedSensors[sensorMAC] {
			continue
		}
		processedSensors[sensorMAC] = true

		// Calculate weighted average temperature using controller's method
		xiaomiTemp, err := x.controller.getWeightedAverageTemperature(ctx, sensorMAC)
		if err != nil {
			x.logger.Debug("skipping Xiaomi average for sensor (no data available)",
				zap.String("sensor_mac", sensorMAC),
				zap.String("room_name", mapping.RoomName),
				zap.Error(err),
			)
			continue
		}

		// Get reading count from control buffer for this sensor
		now := time.Now()
		cutoff := now.Add(-60 * time.Second)
		readings := x.controller.controlBuffer.GetReadingsByTimeWindow(ctx, cutoff, now)
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

		x.controller.metricsBuffer.Add(ctx, weightedAvgReading)
		pushedCount++

		x.logger.Debug("pushed Xiaomi weighted average to metrics buffer",
			zap.String("sensor_mac", sensorMAC),
			zap.String("room_name", mapping.RoomName),
			zap.Float64("weighted_avg", xiaomiTemp),
			zap.Int("reading_count", readingCount),
		)
	}

	span.SetAttributes(attribute.Int("pushed_count", pushedCount))

	x.logger.Debug("finished calculating and pushing Xiaomi weighted averages",
		zap.Int("total_pushed", pushedCount),
	)

	return pushedCount
}
