package control

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// getWeightedAverageTemperature calculates the weighted average temperature from sensor readings in the last 60 seconds
func (c *Controller) getWeightedAverageTemperature(ctx context.Context, sensorMAC string) (float64, error) {
	_, span := c.tracer.Start(ctx, "get_weighted_average_temperature",
		trace.WithAttributes(attribute.String("sensor_mac", sensorMAC)),
	)
	defer span.End()

	now := time.Now()
	cutoff := now.Add(-60 * time.Second)

	// Get readings from control buffer
	readings := c.controlBuffer.GetReadingsByTimeWindow(ctx, cutoff, now)

	if len(readings) == 0 {
		return 0, fmt.Errorf("no sensor readings in last 60 seconds")
	}

	// Filter readings for this sensor MAC
	var sensorReadings []buffer.TimestampedReading
	for _, reading := range readings {
		if reading.Type == buffer.ReadingTypeBLE && reading.BLE != nil {
			if strings.ToUpper(reading.BLE.MAC) == sensorMAC {
				if ts, ok := reading.BLE.Timestamp.(time.Time); ok {
					sensorReadings = append(sensorReadings, buffer.TimestampedReading{
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

	// Calculate weighted average using shared implementation
	weightedAvg := buffer.CalculateWeightedAverage(sensorReadings, cutoff, now)

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
