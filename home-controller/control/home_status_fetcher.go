package control

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// HomeStatusFetcher handles fetching home status and adding to metrics buffer
type HomeStatusFetcher struct {
	controller *Controller
	logger     *zap.Logger
	tracer     trace.Tracer
}

// NewHomeStatusFetcher creates a new home status fetcher
func NewHomeStatusFetcher(controller *Controller, logger *zap.Logger, tracer trace.Tracer) *HomeStatusFetcher {
	return &HomeStatusFetcher{
		controller: controller,
		logger:     logger,
		tracer:     tracer,
	}
}

// Run executes the home status fetch (called by scheduler every minute at :00)
func (f *HomeStatusFetcher) Run(ctx context.Context) {
	ctx, span := f.tracer.Start(ctx, "home_status_fetch",
		trace.WithAttributes(
			attribute.String("home_id", f.controller.homeID),
		),
	)
	defer span.End()

	fetchStart := time.Now()

	f.logger.Debug("fetching home status",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
	)

	// Step 1: Fetch home status from Netatmo
	homeStatus, err := f.controller.netatmoClient.GetHomeStatus(ctx, f.controller.homeID)

	// Step 2: Store in cached status (even if error occurred)
	f.controller.cachedStatusMu.Lock()
	f.controller.cachedStatus = &CachedHomeStatus{
		HomeStatus: homeStatus,
		FetchTime:  fetchStart,
		FetchError: err,
	}
	f.controller.cachedStatusMu.Unlock()

	if err != nil {
		f.logger.Error("failed to fetch home status",
			zap.Error(err),
			zap.String("trace_id", span.SpanContext().TraceID().String()),
		)
		span.RecordError(err)
		return
	}

	span.SetAttributes(
		attribute.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
	)

	f.logger.Info("home status fetched successfully",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("rooms_count", len(homeStatus.Body.Home.Rooms)),
		zap.Duration("fetch_duration", time.Since(fetchStart)),
	)

	// Step 3: Add Netatmo data to metrics buffer
	f.controller.addToMetricsBuffer(ctx, homeStatus)

	f.logger.Debug("home status fetch completed",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Duration("total_duration", time.Since(fetchStart)),
	)
}

// GetCachedHomeStatus returns the cached home status (thread-safe)
// Returns nil if no status has been fetched yet or if the status is too old
func (c *Controller) GetCachedHomeStatus() *CachedHomeStatus {
	c.cachedStatusMu.RLock()
	defer c.cachedStatusMu.RUnlock()

	if c.cachedStatus == nil {
		return nil
	}

	// Return cached status if it's from the same minute
	now := time.Now()
	if c.cachedStatus.FetchTime.Minute() == now.Minute() &&
		c.cachedStatus.FetchTime.Hour() == now.Hour() &&
		c.cachedStatus.FetchTime.Day() == now.Day() {
		return c.cachedStatus
	}

	return nil
}
