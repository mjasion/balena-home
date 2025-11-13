package power

import (
	"context"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"go.uber.org/zap"
)

// Poller periodically scrapes power meter data and adds it to the buffer
type Poller struct {
	scraper *Scraper
	buffer  *buffer.RingBuffer
	logger  *zap.Logger
}

// NewPoller creates a new power meter poller
func NewPoller(scraper *Scraper, buf *buffer.RingBuffer, logger *zap.Logger) *Poller {
	return &Poller{
		scraper: scraper,
		buffer:  buf,
		logger:  logger,
	}
}

// Run executes a single scrape iteration (called by scheduler)
func (p *Poller) Run(ctx context.Context) {
	p.scrapeAndBuffer(ctx)
}

// scrapeAndBuffer scrapes power meter data and adds it to the buffer
func (p *Poller) scrapeAndBuffer(ctx context.Context) {
	result, err := p.scraper.Scrape(ctx)
	if err != nil {
		p.logger.Error("failed to scrape power meter data",
			zap.Error(err),
		)
		return
	}

	if len(result.Readings) == 0 {
		p.logger.Debug("no power readings returned")
		return
	}

	// Convert power readings to buffer readings and add to buffer
	for _, reading := range result.Readings {
		bufferReading := &buffer.Reading{
			Type: buffer.ReadingTypePower,
			Power: &buffer.PowerReading{
				Timestamp:  reading.Timestamp,
				SensorID:   reading.SensorID,
				SensorType: reading.SensorType,
				RoomName:   reading.RoomName,
				Value:      reading.Value,
			},
		}
		p.buffer.Add(bufferReading)

		p.logger.Debug("added power reading to buffer",
			zap.Int("sensor_id", reading.SensorID),
			zap.String("sensor_type", reading.SensorType),
			zap.Float64("value", reading.Value),
			zap.Time("timestamp", reading.Timestamp),
		)
	}

	// Count sensor types for summary logging
	typeCount := make(map[string]int)
	for _, reading := range result.Readings {
		typeCount[reading.SensorType]++
	}

	p.logger.Info("scraped and buffered power meter data",
		zap.Int("reading_count", len(result.Readings)),
		zap.Any("sensor_types", typeCount),
	)
}
