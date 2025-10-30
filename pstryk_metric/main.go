package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mjasion/balena-home/pstryk_metric/buffer"
	"github.com/mjasion/balena-home/pstryk_metric/config"
	"github.com/mjasion/balena-home/pstryk_metric/metrics"
	"github.com/mjasion/balena-home/pstryk_metric/scraper"
	"github.com/mjasion/balena-home/pstryk_metric/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func main() {
	// Parse command-line flags
	configPath := flag.String("c", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		panic("Failed to load configuration: " + err.Error())
	}

	// Initialize logger
	logger, err := cfg.NewLogger()
	if err != nil {
		panic("Failed to create logger: " + err.Error())
	}
	defer logger.Sync()

	logger.Info("Loading configuration", zap.String("path", *configPath))
	logger.Info("Configuration loaded successfully", zap.Any("config", cfg.Redacted()))

	// Initialize OpenTelemetry providers
	ctx := context.Background()
	otelProviders, err := telemetry.InitProviders(ctx, cfg, logger)
	if err != nil {
		logger.Fatal("Failed to initialize OpenTelemetry providers", zap.Error(err))
	}
	if otelProviders != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := otelProviders.Shutdown(shutdownCtx); err != nil {
				logger.Error("Error shutting down OpenTelemetry providers", zap.Error(err))
			}
		}()
	}

	// Get tracer for instrumentation
	tracer := otel.Tracer("pstryk-metric")

	// Initialize components
	logger.Info("Initializing components",
		zap.String("scrapeURL", cfg.ScrapeURL),
		zap.Float64("scrapeTimeoutSeconds", cfg.ScrapeTimeoutSeconds),
		zap.Int("bufferSize", cfg.BufferSize))
	scr := scraper.New(cfg.ScrapeURL, time.Duration(cfg.ScrapeTimeoutSeconds*float64(time.Second)), logger)
	buf := buffer.New(cfg.BufferSize, logger)
	pusher := metrics.New(cfg.PrometheusURL, cfg.PrometheusUsername, cfg.PrometheusPassword, cfg.MetricName, logger)
	healthChecker := metrics.NewHealthChecker(
		buf,
		pusher,
		time.Duration(cfg.ScrapeIntervalSeconds)*time.Second,
		cfg.HealthCheckPort,
		logger,
	)
	logger.Info("Components initialized successfully",
		zap.Int("healthCheckPort", cfg.HealthCheckPort))

	// Start health check server in background
	go func() {
		if err := healthChecker.Start(); err != nil {
			logger.Error("Health check server error", zap.Error(err))
		}
	}()

	// Wait for even second if configured
	if cfg.StartAtEvenSecond {
		waitForEvenSecond(logger)
	}

	// Set up context for graceful shutdown
	appCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start scraping goroutine
	scrapeTicker := time.NewTicker(time.Duration(cfg.ScrapeIntervalSeconds) * time.Second)
	defer scrapeTicker.Stop()

	// Start pushing goroutine
	pushTicker := time.NewTicker(time.Duration(cfg.PushIntervalSeconds) * time.Second)
	defer pushTicker.Stop()

	logger.Info("Service started",
		zap.Int("scrapeIntervalSeconds", cfg.ScrapeIntervalSeconds),
		zap.Int("pushIntervalSeconds", cfg.PushIntervalSeconds))

	// Main event loop
	for {
		select {
		case <-scrapeTicker.C:
			go handleScrape(appCtx, scr, buf, logger, tracer)

		case <-pushTicker.C:
			go handlePush(appCtx, pusher, buf, logger, tracer)

		case sig := <-sigChan:
			logger.Info("Received shutdown signal", zap.String("signal", sig.String()))
			cancel()
			healthChecker.Stop()
			logger.Info("Shutdown complete")
			return
		}
	}
}

// waitForEvenSecond waits until the next even second
func waitForEvenSecond(logger *zap.Logger) {
	now := time.Now()
	currentSecond := now.Second()

	// Calculate next even second
	var waitDuration time.Duration
	if currentSecond%2 == 0 {
		// Already at even second, wait for next even second
		waitDuration = time.Until(now.Truncate(time.Second).Add(2 * time.Second))
	} else {
		// Wait until next second (which will be even)
		waitDuration = time.Until(now.Truncate(time.Second).Add(time.Second))
	}

	logger.Info("Waiting to start at even second", zap.Duration("waitDuration", waitDuration))
	time.Sleep(waitDuration)
	logger.Info("Starting at even second", zap.Int("second", time.Now().Second()))
}

// handleScrape performs a scrape operation
func handleScrape(ctx context.Context, scr *scraper.Scraper, buf *buffer.RingBuffer, logger *zap.Logger, tracer trace.Tracer) {
	ctx, span := tracer.Start(ctx, "scrape")
	defer span.End()

	start := time.Now()
	result, err := scr.Scrape(ctx)

	duration := time.Since(start)
	span.SetAttributes(attribute.Int64("duration_ms", duration.Milliseconds()))

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.ErrorWithTrace(ctx, logger, "Scrape failed", zap.Duration("duration", duration), zap.Error(err))
	} else {
		span.SetAttributes(attribute.Int("reading_count", len(result.Readings)))
		span.SetStatus(codes.Ok, "scrape successful")
		telemetry.InfoWithTrace(ctx, logger, "Scrape successful",
			zap.Duration("duration", duration),
			zap.Int("readingCount", len(result.Readings)))
		buf.Add(result)
	}
}

// handlePush performs a push operation
func handlePush(ctx context.Context, pusher *metrics.Pusher, buf *buffer.RingBuffer, logger *zap.Logger, tracer trace.Tracer) {
	ctx, span := tracer.Start(ctx, "push")
	defer span.End()

	results := buf.GetAll()
	span.SetAttributes(attribute.Int("buffer_size", len(results)))

	if len(results) == 0 {
		span.SetStatus(codes.Ok, "no data to push")
		telemetry.WarnWithTrace(ctx, logger, "No data to push")
		return
	}

	err := pusher.Push(ctx, results)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.ErrorWithTrace(ctx, logger, "Push failed", zap.Error(err))
	} else {
		span.SetAttributes(attribute.Int("cleared_results", len(results)))
		span.SetStatus(codes.Ok, "push successful")
		telemetry.InfoWithTrace(ctx, logger, "Push operation completed, clearing buffer",
			zap.Int("clearedResults", len(results)))
		buf.Clear()
	}
}
