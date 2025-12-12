package control

import (
	"context"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"go.uber.org/zap"
)

// pushXiaomiTemperatureMetric pushes just the Xiaomi temperature metric to the metrics buffer
// This is called immediately after fetching Xiaomi temperature in evaluateRoom
// It pushes thermostat_control_xiaomi_temperature_celsius metric for every room evaluation
func (c *Controller) pushXiaomiTemperatureMetric(ctx context.Context, roomName string, xiaomiTemp float64) {
	// Create a minimal control reading with just the Xiaomi temperature
	reading := &buffer.Reading{
		Type: buffer.ReadingTypeControl,
		Control: &buffer.ControlReading{
			Timestamp:         time.Now(),
			RoomName:          roomName,
			XiaomiTemperature: xiaomiTemp,
		},
	}

	// Push to metrics buffer (not control buffer - this is for Prometheus)
	if c.metricsBuffer != nil {
		c.metricsBuffer.Add(ctx, reading)
	}

	c.logger.Debug("pushed Xiaomi temperature metric",
		zap.String("room_name", roomName),
		zap.Float64("xiaomi_temp", xiaomiTemp),
	)
}
