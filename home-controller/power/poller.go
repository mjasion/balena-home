package power

import (
	"context"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
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

	// Create ticker for periodic scraping
	ticker := time.NewTicker(p.scrapeInterval)
	defer ticker.Stop()

	// Scrape immediately on start
	p.scrapeAndBuffer(ctx)

	// Then scrape at regular intervals
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
				Timestamp: reading.Timestamp,
				SensorID:  reading.SensorID,
				Value:     reading.Value,
			},
		}
		p.buffer.Add(bufferReading)

		p.logger.Debug("added power reading to buffer",
			zap.Int("sensor_id", reading.SensorID),
			zap.Float64("value_watts", reading.Value),
			zap.Time("timestamp", reading.Timestamp),
		)
	}

	p.logger.Info("scraped and buffered power meter data",
		zap.Int("reading_count", len(result.Readings)),
	)
}
