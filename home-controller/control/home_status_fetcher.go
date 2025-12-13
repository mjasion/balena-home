package control

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// MetricJob handles fetching home status and storing to shared state
type MetricJob struct {
	controller *Controller
	logger     *zap.Logger
	tracer     trace.Tracer
}

// NewMetricJob creates a new metric job
func NewMetricJob(controller *Controller, logger *zap.Logger, tracer trace.Tracer) *MetricJob {
	return &MetricJob{
		controller: controller,
		logger:     logger,
		tracer:     tracer,
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

	// Push Xiaomi temperature metrics immediately after fetching home status
	// This calculates and pushes the temperature difference for all rooms
	m.controller.pushAllXiaomiTemperatureMetrics(ctx, homeStatus)

	// Store home status in shared state for Control Job first
	// Includes trace ID for correlation between Metric Job and Control Job
	traceID := span.SpanContext().TraceID().String()
	m.controller.sharedHomeStatus.Set(homeStatus, traceID)

	span.SetAttributes(attribute.Bool("stored_to_shared_state", true))

	m.logger.Debug("metric job - stored home status in shared state",
		zap.String("trace_id", traceID),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)

	// Add Netatmo data to metrics buffer for Prometheus push
	m.logger.Debug("metric job - adding Netatmo data to metrics buffer",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)

	m.controller.addToMetricsBuffer(ctx, homeStatus)

	totalDuration := time.Since(fetchStart)
	span.SetAttributes(attribute.Int64("total_duration_ms", totalDuration.Milliseconds()))

	m.logger.Info("metric job completed - home status stored and Netatmo metrics added to buffer",
		zap.String("trace_id", traceID),
		zap.Duration("total_duration", totalDuration),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)
}

// Deprecated: HomeStatusFetcher - Use MetricJob instead
type HomeStatusFetcher = MetricJob
