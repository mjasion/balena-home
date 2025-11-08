package power

import (
	"context"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/scheduler"
	"go.uber.org/zap"
)

// Poller periodically scrapes power meter data and adds it to the buffer
type Poller struct {
	scraper        *Scraper
	buffer         *buffer.RingBuffer
	logger         *zap.Logger
	scrapeInterval time.Duration
}

// NewPoller creates a new power meter poller
func NewPoller(scraper *Scraper, buf *buffer.RingBuffer, scrapeIntervalSeconds int, logger *zap.Logger) *Poller {
	return &Poller{
		scraper:        scraper,
		buffer:         buf,
		logger:         logger,
		scrapeInterval: time.Duration(scrapeIntervalSeconds) * time.Second,
	}
}

// Start starts the polling loop
func (p *Poller) Start(ctx context.Context) {
	p.logger.Info("starting power meter poller",
		zap.Duration("scrape_interval", p.scrapeInterval),
	)

	// Wait for first aligned interval
	initialWait := scheduler.WaitForAlignedInterval(p.scrapeInterval, p.logger)
	select {
	case <-ctx.Done():
		p.logger.Info("power meter poller stopped before first scrape")
		return
	case <-time.After(initialWait):
		p.scrapeAndBuffer(ctx)
	}

	// Now use ticker for subsequent scrapes
	ticker := time.NewTicker(p.scrapeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("stopping power meter poller")
			return
		case <-ticker.C:
			p.scrapeAndBuffer(ctx)
		}
	}
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
				SensorName: reading.SensorName,
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
