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
	controller     *Controller
	logger         *zap.Logger
	tracer         trace.Tracer
	homeStatusChan chan<- *netatmo.HomeStatusResponse
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
	// Create a new root trace span for this job execution
	ctx, span := m.tracer.Start(ctx, "metric_job",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("home_id", m.controller.homeID),
			attribute.String("job", "metric_job"),
			attribute.String("operation", "fetch_home_status"),
		),
	)
	defer span.End()

	fetchStart := time.Now()

	m.logger.Info("metric job started - fetching home status from Netatmo API",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
		zap.String("home_id", m.controller.homeID),
	)

	// Fetch home status from Netatmo
	homeStatus, err := m.controller.netatmoClient.GetHomeStatus(ctx, m.controller.homeID)

	if err != nil {
		m.logger.Error("metric job failed - could not fetch home status from Netatmo API",
			zap.Error(err),
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("home_id", m.controller.homeID),
		)
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("fetch_success", false))
		return
	}

	fetchDuration := time.Since(fetchStart)

	span.SetAttributes(
		attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		attribute.Bool("fetch_success", true),
		attribute.Int64("fetch_duration_ms", fetchDuration.Milliseconds()),
	)

	m.logger.Info("metric job - home status fetched successfully from Netatmo API",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		zap.Duration("fetch_duration", fetchDuration),
	)

	// Add Netatmo data to metrics buffer for Prometheus push
	m.logger.Debug("metric job - adding Netatmo data to metrics buffer",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)
	m.controller.addToMetricsBuffer(ctx, homeStatus)

	// Send status to Control Job via channel
	// Note: Channel has buffer size 1 to allow Metric Job to never block
	// Control Job must check timestamp to ensure data freshness
	select {
	case m.homeStatusChan <- homeStatus:
		totalDuration := time.Since(fetchStart)
		span.SetAttributes(
			attribute.Bool("sent_to_control_job", true),
			attribute.Int64("total_duration_ms", totalDuration.Milliseconds()),
		)
		m.logger.Info("metric job completed - home status sent to control job via channel",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Duration("total_duration", totalDuration),
		)
	default:
		// Channel is full - this means Control Job hasn't consumed previous data yet
		// This is expected if Control Job runs less frequently than Metric Job
		totalDuration := time.Since(fetchStart)
		span.SetAttributes(
			attribute.Bool("sent_to_control_job", false),
			attribute.String("skip_reason", "channel_full"),
			attribute.Int64("total_duration_ms", totalDuration.Milliseconds()),
		)
		m.logger.Warn("metric job - channel full, control job hasn't consumed previous data yet (this is expected between control job runs)",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Duration("total_duration", totalDuration),
		)
	}
}

// Deprecated: HomeStatusFetcher - Use MetricJob instead
type HomeStatusFetcher = MetricJob
