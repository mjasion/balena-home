package netatmo

import (
	"context"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"go.uber.org/zap"
)

// Poller periodically fetches thermostat data from Netatmo and adds it to the buffer
type Poller struct {
	client *Client
	buffer *buffer.RingBuffer
	logger *zap.Logger
}

// NewPoller creates a new Netatmo poller
func NewPoller(client *Client, buf *buffer.RingBuffer, logger *zap.Logger) *Poller {
	return &Poller{
		client: client,
		buffer: buf,
		logger: logger,
	}
}

// Run executes a single poll iteration (called by scheduler)
func (p *Poller) Run(ctx context.Context) {
	p.fetchAndBuffer(ctx)
}

// fetchAndBuffer fetches thermostat data and adds it to the buffer
func (p *Poller) fetchAndBuffer(ctx context.Context) {
	readings, err := p.client.FetchAllThermostats(ctx)
	if err != nil {
		p.logger.Error("failed to fetch Netatmo data",
			zap.Error(err),
		)
		return
	}

	if len(readings) == 0 {
		p.logger.Debug("no Netatmo readings returned")
		return
	}

	// Convert Netatmo readings to buffer readings and add to buffer
	for _, reading := range readings {
		bufferReading := &buffer.Reading{
			Type: buffer.ReadingTypeNetatmo,
			Thermostat: &buffer.ThermostatReading{
				Timestamp:           time.Unix(reading.Timestamp, 0),
				HomeID:              reading.HomeID,
				HomeName:            reading.HomeName,
				RoomID:              reading.RoomID,
				RoomName:            reading.RoomName,
				MeasuredTemperature: reading.MeasuredTemperature,
				SetpointTemperature: reading.SetpointTemperature,
				SetpointMode:        reading.SetpointMode,
				HeatingPowerRequest: reading.HeatingPowerRequest,
				OpenWindow:          reading.OpenWindow,
				Reachable:           reading.Reachable,
			},
		}
		p.buffer.Add(bufferReading)

		p.logger.Debug("added Netatmo reading to buffer",
			zap.String("home", reading.HomeName),
			zap.String("room", reading.RoomName),
			zap.Float64("measured_temp", reading.MeasuredTemperature),
			zap.Float64("setpoint_temp", reading.SetpointTemperature),
			zap.String("mode", reading.SetpointMode),
			zap.Int("heating_power", reading.HeatingPowerRequest),
		)
	}

	p.logger.Info("fetched and buffered Netatmo data",
		zap.Int("reading_count", len(readings)),
	)
}
