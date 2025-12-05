package control

import (
	"context"
	"time"

	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// MetricJob handles fetching home status and sending to Control Job via channel
type MetricJob struct {
	controller       *Controller
	logger           *zap.Logger
	tracer           trace.Tracer
	homeStatusChan   chan<- *netatmo.HomeStatusResponse
}

// NewMetricJob creates a new metric job
func NewMetricJob(controller *Controller, logger *zap.Logger, tracer trace.Tracer, homeStatusChan chan<- *netatmo.HomeStatusResponse) *MetricJob {
	return &MetricJob{
		controller:     controller,
		logger:         logger,
		tracer:         tracer,
		homeStatusChan: homeStatusChan,
	}
}

// Run executes the metric job (called by scheduler every minute)
func (m *MetricJob) Run(ctx context.Context) {
	ctx, span := m.tracer.Start(ctx, "metric_job_fetch_home_status",
		trace.WithAttributes(
			attribute.String("home_id", m.controller.homeID),
		),
	)
	defer span.End()

	fetchStart := time.Now()

	m.logger.Debug("fetching home status for metrics",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
	)

	// Fetch home status from Netatmo
	homeStatus, err := m.controller.netatmoClient.GetHomeStatus(ctx, m.controller.homeID)

	if err != nil {
		m.logger.Error("failed to fetch home status",
			zap.Error(err),
			zap.String("trace_id", span.SpanContext().TraceID().String()),
		)
		span.RecordError(err)
		return
	}

	span.SetAttributes(
		attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)

	m.logger.Debug("home status fetched successfully",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		zap.Duration("fetch_duration", time.Since(fetchStart)),
	)

	// Add Netatmo data to metrics buffer
	m.controller.addToMetricsBuffer(ctx, homeStatus)

	// Send status to Control Job via channel (non-blocking with buffered channel)
	select {
	case m.homeStatusChan <- homeStatus:
		m.logger.Debug("sent home status to control job",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Duration("total_duration", time.Since(fetchStart)),
		)
	case <-ctx.Done():
		m.logger.Warn("context cancelled before sending home status")
	default:
		// Channel is full, this shouldn't happen with buffered channel but log it
		m.logger.Warn("home status channel is full, dropping oldest data")
		select {
		case <-m.homeStatusChan:
		default:
		}
		m.homeStatusChan <- homeStatus
	}
}

// Deprecated: HomeStatusFetcher - Use MetricJob instead
type HomeStatusFetcher = MetricJob

