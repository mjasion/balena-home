package control

import (
	"context"
	"fmt"
	"math"
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
	weightedAvg := calculateWeightedAverage(sensorReadings, cutoff, now)

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

// calculateWeightedAverage calculates the weighted average of sensor readings
// weight = (timestamp - cutoffTime) / (now - cutoffTime)
func calculateWeightedAverage(readings []SensorReading, cutoff, now time.Time) float64 {
	var weightedSum float64
	var totalWeight float64
	timeRange := now.Sub(cutoff).Seconds()

	for _, reading := range readings {
		weight := reading.Timestamp.Sub(cutoff).Seconds() / timeRange
		weightedSum += reading.Temperature * weight
		totalWeight += weight
	}

	var weightedAvg float64
	if totalWeight == 0 {
		// Fallback: use simple average
		var sum float64
		for _, reading := range readings {
			sum += reading.Temperature
		}
		weightedAvg = sum / float64(len(readings))
	} else {
		weightedAvg = weightedSum / totalWeight
	}

	// Round to 2 decimal places
	return math.Round(weightedAvg*100) / 100
}
