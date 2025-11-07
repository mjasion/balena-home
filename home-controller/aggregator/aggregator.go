package aggregator

import (
	"context"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/scanner"
	"go.uber.org/zap"
)

// Aggregator calculates weighted averages for BLE sensor readings
type Aggregator struct {
	sensors       []scanner.SensorConfig
	controlBuffer *buffer.RingBuffer // Source buffer with BLE readings
	metricsBuffer *buffer.RingBuffer // Destination buffer for metrics
	logger        *zap.Logger
	interval      time.Duration
}

// New creates a new sensor data aggregator
func New(
	sensors []scanner.SensorConfig,
	controlBuffer *buffer.RingBuffer,
	metricsBuffer *buffer.RingBuffer,
	intervalSeconds int,
	logger *zap.Logger,
) *Aggregator {
	return &Aggregator{
		sensors:       sensors,
		controlBuffer: controlBuffer,
		metricsBuffer: metricsBuffer,
		logger:        logger,
		interval:      time.Duration(intervalSeconds) * time.Second,
	}
}

// Start begins the aggregation loop
func (a *Aggregator) Start(ctx context.Context) error {
	a.logger.Info("starting BLE aggregator",
		zap.Duration("interval", a.interval),
		zap.Int("sensor_count", len(a.sensors)),
	)

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.logger.Info("BLE aggregator stopped")
			return nil
		case <-ticker.C:
			a.calculateWeightedAverages()
		}
	}
}

// calculateWeightedAverages calculates weighted averages for all sensors
func (a *Aggregator) calculateWeightedAverages() {
	now := time.Now()
	cutoff := now.Add(-60 * time.Second)

	// Get readings from control buffer (last 60 seconds)
	readings := a.controlBuffer.GetReadingsByTimeWindow(cutoff, now)

	if len(readings) == 0 {
		a.logger.Debug("no readings available for weighted average calculation")
		return
	}

	// Process each configured sensor
	for _, sensor := range a.sensors {
		sensorMAC := strings.ToUpper(strings.TrimSpace(sensor.MACAddress))

		// Filter readings for this sensor
		var sensorReadings []sensorReading
		for _, reading := range readings {
			if reading.Type == buffer.ReadingTypeBLE && reading.BLE != nil {
				if strings.ToUpper(reading.BLE.MAC) == sensorMAC {
					if ts, ok := reading.BLE.Timestamp.(time.Time); ok {
						sensorReadings = append(sensorReadings, sensorReading{
							Timestamp:   ts,
							Temperature: reading.BLE.TemperatureCelsius,
						})
					}
				}
			}
		}

		if len(sensorReadings) == 0 {
			a.logger.Debug("no readings found for sensor in last 60 seconds",
				zap.String("sensor_name", sensor.Name),
				zap.String("sensor_mac", sensorMAC),
			)
			continue
		}

		// Calculate weighted average
		weightedAvg := a.calculateWeightedAverage(sensorReadings, cutoff, now)

		// Create weighted average reading
		avgReading := &buffer.Reading{
			Type: buffer.ReadingTypeBLEWeightedAvg,
			WeightedAvg: &buffer.WeightedAvgReading{
				Timestamp:          now,
				MAC:                sensorMAC,
				SensorName:         sensor.Name,
				SensorID:           sensor.ID,
				TemperatureCelsius: weightedAvg,
				ReadingCount:       len(sensorReadings),
			},
		}

		// Push to metrics buffer
		a.metricsBuffer.Add(avgReading)

		a.logger.Debug("calculated weighted average temperature",
			zap.String("sensor_name", sensor.Name),
			zap.String("sensor_mac", sensorMAC),
			zap.Int("reading_count", len(sensorReadings)),
			zap.Float64("weighted_average", weightedAvg),
		)
	}
}

// calculateWeightedAverage calculates time-weighted average
// Recent readings have higher weight than older readings
func (a *Aggregator) calculateWeightedAverage(readings []sensorReading, cutoff, now time.Time) float64 {
	if len(readings) == 0 {
		return 0
	}

	// Calculate weighted average
	// weight = (timestamp - cutoffTime) / (now - cutoffTime)
	var weightedSum float64
	var totalWeight float64
	timeRange := now.Sub(cutoff).Seconds()

	for _, reading := range readings {
		weight := reading.Timestamp.Sub(cutoff).Seconds() / timeRange
		weightedSum += reading.Temperature * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		// Fallback: use simple average
		var sum float64
		for _, reading := range readings {
			sum += reading.Temperature
		}
		return sum / float64(len(readings))
	}

	return weightedSum / totalWeight
}

// sensorReading is a lightweight struct for calculations
type sensorReading struct {
	Timestamp   time.Time
	Temperature float64
}
