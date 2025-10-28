package netatmo

import (
	"context"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"go.uber.org/zap"
)

// Poller periodically fetches thermostat data from Netatmo and adds it to the buffer
type Poller struct {
	fetcher      *Fetcher
	buffer       *buffer.RingBuffer
	logger       *zap.Logger
	fetchInterval time.Duration
}

// NewPoller creates a new Netatmo poller
func NewPoller(fetcher *Fetcher, buf *buffer.RingBuffer, fetchIntervalSeconds int, logger *zap.Logger) *Poller {
	return &Poller{
		fetcher:      fetcher,
		buffer:       buf,
		logger:       logger,
		fetchInterval: time.Duration(fetchIntervalSeconds) * time.Second,
	}
}

// Start starts the polling loop
func (p *Poller) Start(ctx context.Context) {
	p.logger.Info("starting Netatmo poller",
		zap.Duration("fetch_interval", p.fetchInterval),
	)

	// Create ticker for periodic fetching
	ticker := time.NewTicker(p.fetchInterval)
	defer ticker.Stop()

	// Fetch immediately on start
	p.fetchAndBuffer(ctx)

	// Then fetch at regular intervals
	for {
		select {
		case <-ctx.Done():
			p.logger.Info("stopping Netatmo poller")
			return
		case <-ticker.C:
			p.fetchAndBuffer(ctx)
		}
	}
}

// fetchAndBuffer fetches thermostat data and adds it to the buffer
func (p *Poller) fetchAndBuffer(ctx context.Context) {
	readings, err := p.fetcher.FetchAllThermostats(ctx)
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
