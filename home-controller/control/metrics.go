package control

import (
	"context"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"go.uber.org/zap"
)

// ControlMetrics represents metrics for a control decision that should be pushed to Prometheus
type ControlMetrics struct {
	Timestamp             time.Time
	RoomName              string
	Action                string // "skip", "no_adjustment_needed", "set_manual_override"
	XiaomiTemperature     float64
	ScheduledTemperature  float64
	ThermostatMeasured    float64
	CalculatedSetpoint    float64
	TemperatureDifference float64 // xiaomiTemp - thermostatMeasured
	SetpointAdjustment    float64 // calculatedSetpoint - thermostatMeasured
	ExternallyModified    bool
	HardOverrideActive    bool
}

// pushXiaomiTemperatureMetric pushes just the Xiaomi temperature metric to the metrics buffer
// This is called immediately after fetching Xiaomi temperature in evaluateRoom
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

// pushControlMetrics pushes control decision metrics to the metrics buffer
func (c *Controller) pushControlMetrics(ctx context.Context, decision ControlDecision, hardOverrideActive bool, externallyModified bool) {
	// Skip pushing metrics if XiaomiTemperature is 0 (indicates error or no sensor data)
	// This prevents pushing invalid metrics when sensor data is unavailable
	if decision.XiaomiTemperature == 0 {
		c.logger.Debug("skipping control metrics push: sensor temperature is 0 (no data or error)",
			zap.String("room_name", decision.RoomName),
		)
		return
	}

	// Calculate derived metrics
	tempDiff := decision.XiaomiTemperature - decision.ThermostatMeasured
	setpointAdj := 0.0
	if decision.Action == "set_manual_override" {
		setpointAdj = decision.CalculatedSetpoint - decision.ThermostatMeasured
	}

	// Create control metrics reading
	reading := &buffer.Reading{
		Type: buffer.ReadingTypeControl,
		Control: &buffer.ControlReading{
			Timestamp:             time.Now(),
			RoomName:              decision.RoomName,
			Action:                decision.Action,
			XiaomiTemperature:     decision.XiaomiTemperature,
			ScheduledTemperature:  decision.ScheduledTemp,
			ThermostatMeasured:    decision.ThermostatMeasured,
			ThermostatMode:        decision.ThermostatMode,
			CalculatedSetpoint:    decision.CalculatedSetpoint,
			TemperatureDifference: tempDiff,
			SetpointAdjustment:    setpointAdj,
			ExternallyModified:    externallyModified,
			HardOverrideActive:    hardOverrideActive,
		},
	}

	// Push to metrics buffer (not control buffer - this is for Prometheus)
	if c.metricsBuffer != nil {
		c.metricsBuffer.Add(ctx, reading)
	}
}
