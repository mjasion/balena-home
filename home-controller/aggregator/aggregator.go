package aggregator

import (
	"context"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/scanner"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Aggregator calculates weighted averages for BLE sensor readings
type Aggregator struct {
	sensors       []scanner.SensorConfig
	controlBuffer *buffer.RingBuffer // Source buffer with BLE readings
	metricsBuffer *buffer.RingBuffer // Destination buffer for metrics
	logger        *zap.Logger
	tracer        trace.Tracer
}

// New creates a new sensor data aggregator
func New(
	sensors []scanner.SensorConfig,
	controlBuffer *buffer.RingBuffer,
	metricsBuffer *buffer.RingBuffer,
	logger *zap.Logger,
) *Aggregator {
	return &Aggregator{
		sensors:       sensors,
		controlBuffer: controlBuffer,
		metricsBuffer: metricsBuffer,
		logger:        logger,
		tracer:        otel.Tracer("home-controller/aggregator"),
	}
}

// Run executes a single aggregation iteration (called by scheduler)
func (a *Aggregator) Run(ctx context.Context) {
	ctx, span := a.tracer.Start(ctx, "ble_aggregator_job",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("job", "ble_aggregator"),
		),
	)
	defer span.End()

	a.logger.Debug("starting BLE aggregator job",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
	)

	a.calculateWeightedAverages(ctx, span)
}

// calculateWeightedAverages calculates weighted averages for all sensors
func (a *Aggregator) calculateWeightedAverages(ctx context.Context, parentSpan trace.Span) {
	now := time.Now()
	cutoff := now.Add(-60 * time.Second)

	// Get readings from control buffer (last 60 seconds)
	readings := a.controlBuffer.GetReadingsByTimeWindow(ctx, cutoff, now)

	if len(readings) == 0 {
		a.logger.Debug("no readings available for weighted average calculation",
			zap.String("trace_id", parentSpan.SpanContext().TraceID().String()),
		)
		parentSpan.SetAttributes(attribute.Int("sensor_count", 0))
		return
	}

	parentSpan.SetAttributes(
		attribute.Int("total_readings", len(readings)),
		attribute.Int("sensor_count", len(a.sensors)),
	)

	sensorsProcessed := 0

	// Process each configured sensor
	for _, sensor := range a.sensors {
		sensorMAC := strings.ToUpper(strings.TrimSpace(sensor.MACAddress))

		// Filter readings for this sensor
		var sensorReadings []buffer.TimestampedReading
		for _, reading := range readings {
			if reading.Type == buffer.ReadingTypeBLE && reading.BLE != nil {
				if strings.ToUpper(reading.BLE.MAC) == sensorMAC {
					if ts, ok := reading.BLE.Timestamp.(time.Time); ok {
						sensorReadings = append(sensorReadings, buffer.TimestampedReading{
							Timestamp:   ts,
							Temperature: reading.BLE.TemperatureCelsius,
							Humidity:    float64(reading.BLE.HumidityPercent),
						})
					}
				}
			}
		}

		if len(sensorReadings) == 0 {
			a.logger.Debug("no readings found for sensor in last 60 seconds",
				zap.String("sensor_name", sensor.Name),
				zap.String("sensor_mac", sensorMAC),
				zap.String("trace_id", parentSpan.SpanContext().TraceID().String()),
			)
			continue
		}

		// Calculate weighted averages using shared implementation
		weightedAvg := buffer.CalculateWeightedAverage(sensorReadings, cutoff, now)
		weightedHumidity := buffer.CalculateWeightedAverageHumidity(sensorReadings, cutoff, now)

		// Create weighted average reading
		avgReading := &buffer.Reading{
			Type: buffer.ReadingTypeBLEWeightedAvg,
			WeightedAvg: &buffer.WeightedAvgReading{
				Timestamp:          now,
				MAC:                sensorMAC,
				RoomName:           sensor.Name,
				SensorID:           sensor.ID,
				TemperatureCelsius: weightedAvg,
				HumidityPercent:    weightedHumidity,
				ReadingCount:       len(sensorReadings),
			},
		}

		// Push to metrics buffer
		a.metricsBuffer.Add(ctx, avgReading)

		sensorsProcessed++

		a.logger.Debug("calculated weighted average temperature",
			zap.String("sensor_name", sensor.Name),
			zap.String("sensor_mac", sensorMAC),
			zap.Int("reading_count", len(sensorReadings)),
			zap.Float64("weighted_average", weightedAvg),
			zap.Float64("weighted_humidity", weightedHumidity),
			zap.String("trace_id", parentSpan.SpanContext().TraceID().String()),
		)
	}

	parentSpan.SetAttributes(attribute.Int("sensors_processed", sensorsProcessed))

	a.logger.Info("completed BLE aggregation",
		zap.Int("sensors_processed", sensorsProcessed),
		zap.String("trace_id", parentSpan.SpanContext().TraceID().String()),
	)
}

