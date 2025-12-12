package control

import (
	"context"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.uber.org/zap"
)

// pushAllXiaomiTemperatureMetrics pushes Xiaomi temperature metrics for all rooms
// Called immediately after fetching home status in runControlLoop
// Calculates temperature difference between Xiaomi sensor and scheduled temperature
func (c *Controller) pushAllXiaomiTemperatureMetrics(ctx context.Context, homeStatus *netatmo.HomeStatusResponse) {
	for _, mapping := range c.config.Mappings {
		// Get Xiaomi sensor reading
		sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
		xiaomiTemp, err := c.getWeightedAverageTemperature(ctx, sensorMAC)
		if err != nil || xiaomiTemp == 0 {
			c.logger.Debug("skipping Xiaomi metric push - sensor data unavailable",
				zap.String("room_name", mapping.RoomName),
				zap.String("sensor_mac", sensorMAC),
				zap.Error(err),
			)
			continue
		}

		// Get room status
		var roomStatus *netatmo.RoomStatus
		for i := range homeStatus.Body.Home.Rooms {
			if homeStatus.Body.Home.Rooms[i].ID == mapping.RoomID {
				roomStatus = &homeStatus.Body.Home.Rooms[i]
				break
			}
		}

		if roomStatus == nil {
			c.logger.Debug("skipping Xiaomi metric push - room not found in home status",
				zap.String("room_name", mapping.RoomName),
				zap.String("room_id", mapping.RoomID),
			)
			continue
		}

		// Calculate scheduled temperature (respects hard overrides)
		scheduledTemp := c.determineScheduledTemp(mapping.RoomName, roomStatus)

		// Calculate temperature difference
		tempDiff := xiaomiTemp - scheduledTemp

		// Create control reading with Xiaomi temperature and temperature difference
		reading := &buffer.Reading{
			Type: buffer.ReadingTypeControl,
			Control: &buffer.ControlReading{
				Timestamp:            time.Now(),
				RoomName:             mapping.RoomName,
				XiaomiTemperature:    xiaomiTemp,
				TemperatureDifference: tempDiff,
			},
		}

		// Push to metrics buffer (not control buffer - this is for Prometheus)
		if c.metricsBuffer != nil {
			c.metricsBuffer.Add(ctx, reading)
		}

		c.logger.Debug("pushed Xiaomi temperature metric with difference",
			zap.String("room_name", mapping.RoomName),
			zap.Float64("xiaomi_temp", xiaomiTemp),
			zap.Float64("scheduled_temp", scheduledTemp),
			zap.Float64("temp_diff", tempDiff),
		)
	}
}
