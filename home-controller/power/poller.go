package power

import (
	"context"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Poller periodically scrapes power meter data and adds it to the buffer
type Poller struct {
	scraper *Scraper
	buffer  *buffer.RingBuffer
	logger  *zap.Logger
	tracer  trace.Tracer
}

// NewPoller creates a new power meter poller
func NewPoller(scraper *Scraper, buf *buffer.RingBuffer, logger *zap.Logger) *Poller {
	return &Poller{
		scraper: scraper,
		buffer:  buf,
		logger:  logger,
		tracer:  otel.Tracer("home-controller/power"),
	}
}

// Run executes a single scrape iteration (called by scheduler)
func (p *Poller) Run(ctx context.Context) {
	ctx, span := p.tracer.Start(ctx, "power_poller_job",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("job", "power_poller"),
		),
	)
	defer span.End()

	p.logger.Debug("starting power poller job",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
	)

	p.scrapeAndBuffer(ctx)
}

// scrapeAndBuffer scrapes power meter data and adds it to the buffer
func (p *Poller) scrapeAndBuffer(ctx context.Context) {
	ctx, span := p.tracer.Start(ctx, "scrape_and_buffer")
	defer span.End()

	result, err := p.scraper.Scrape(ctx)
	if err != nil {
		p.logger.Error("failed to scrape power meter data",
			zap.Error(err),
			zap.String("trace_id", span.SpanContext().TraceID().String()),
		)
		span.RecordError(err)
		return
	}

	if len(result.Readings) == 0 {
		p.logger.Debug("no power readings returned",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
		)
		span.SetAttributes(attribute.Int("reading_count", 0))
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
		p.buffer.Add(ctx, bufferReading)

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

	span.SetAttributes(
		attribute.Int("reading_count", len(result.Readings)),
		attribute.Int("sensor_type_count", len(typeCount)),
	)

	p.logger.Info("scraped and buffered power meter data",
		zap.Int("reading_count", len(result.Readings)),
		zap.Any("sensor_types", typeCount),
		zap.String("trace_id", span.SpanContext().TraceID().String()),
	)
}
