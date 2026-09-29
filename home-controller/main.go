package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mjasion/balena-home/thermostats/aggregator"
	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/metrics"
	homeOtel "github.com/mjasion/balena-home/thermostats/otel"
	"github.com/mjasion/balena-home/thermostats/power"
	"github.com/mjasion/balena-home/thermostats/pyroscope"
	"github.com/mjasion/balena-home/thermostats/scanner"
	"github.com/mjasion/balena-home/thermostats/scheduler"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// Parse command-line flags
	configPath := flag.String("c", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger, err := cfg.InitLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	logger.Info("starting BLE temperature monitoring service")
	cfg.PrintConfig(logger)

	// Initialize Pyroscope profiler if enabled
	profiler, err := pyroscope.New(&cfg.Pyroscope, logger)
	if err != nil {
		logger.Error("failed to initialize pyroscope profiler", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		if err := profiler.Stop(); err != nil {
			logger.Error("failed to stop pyroscope profiler", zap.Error(err))
		}
	}()

	// Initialize OpenTelemetry resource (shared by tracer and log provider)
	res, err := homeOtel.CreateResource(context.Background(), &cfg.OpenTelemetry, logger)
	if err != nil {
		logger.Error("failed to create opentelemetry resource", zap.Error(err))
		os.Exit(1)
	}

	// Initialize OpenTelemetry tracer if enabled
	shutdownTracer, err := homeOtel.InitTracer(context.Background(), &cfg.OpenTelemetry, res, logger)
	if err != nil {
		logger.Error("failed to initialize opentelemetry tracer", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		if err := shutdownTracer(context.Background()); err != nil {
			logger.Error("failed to shutdown opentelemetry tracer", zap.Error(err))
		}
	}()

	// Initialize OpenTelemetry log provider and otelzap bridge
	logProvider, shutdownLogProvider, err := homeOtel.InitLogProvider(context.Background(), &cfg.OpenTelemetry, res, logger)
	if err != nil {
		logger.Error("failed to initialize opentelemetry log provider", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		if err := shutdownLogProvider(context.Background()); err != nil {
			logger.Error("failed to shutdown opentelemetry log provider", zap.Error(err))
		}
	}()

	// Wrap logger with otelzap bridge to send logs via OTLP alongside traces
	if logProvider != nil {
		otelCore := otelzap.NewCore("home-controller",
			otelzap.WithLoggerProvider(logProvider),
		)
		logger = logger.WithOptions(zap.WrapCore(func(c zapcore.Core) zapcore.Core {
			return zapcore.NewTee(c, otelCore)
		}))
		logger.Info("otelzap bridge enabled - logs will be sent via OTLP")
	}

	// Create dual ring buffers for separate purposes
	// Metrics buffer: Used by metrics pusher (cleared every push interval)
	metricsBuffer := buffer.New(cfg.Prometheus.BufferSize, logger)
	logger.Info("metrics buffer created", zap.Int("capacity", cfg.Prometheus.BufferSize))

	// Control buffer: Used by BLE aggregator for weighted averages (auto-cleanup enabled, keeps last 5 minutes)
	controlBufferSize := 10000 // Large capacity, but auto-cleanup keeps last 5 minutes
	controlBuffer := buffer.NewWithAutoCleanup(controlBufferSize, logger)
	logger.Info("control buffer created",
		zap.Int("capacity", controlBufferSize),
		zap.Bool("auto_cleanup", true),
		zap.Duration("retention", 5*time.Minute),
	)

	// Create Prometheus pusher (uses metrics buffer)
	pusher := metrics.New(
		cfg.Prometheus.URL,
		cfg.Prometheus.Username,
		cfg.Prometheus.Password,
		metricsBuffer,
		cfg.Prometheus.PushIntervalSeconds,
		cfg.Prometheus.BatchSize,
		logger,
	)
	logger.Info("prometheus pusher initialized", zap.String("url", cfg.Prometheus.URL))

	// Create job scheduler with UI
	schedulerCfg := &scheduler.Config{
		UIEnabled: cfg.Scheduler.UIEnabled,
		UIHost:    cfg.Scheduler.UIHost,
		UIPort:    cfg.Scheduler.UIPort,
	}
	jobScheduler, err := scheduler.New(schedulerCfg, logger)
	if err != nil {
		logger.Error("failed to create job scheduler", zap.Error(err))
		os.Exit(1)
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create wait group for goroutines (only for BLE scanner now)
	var wg sync.WaitGroup

	// Convert config sensors to scanner format
	scannerSensors := make([]scanner.SensorConfig, len(cfg.BLE.Sensors))
	for i, sensor := range cfg.BLE.Sensors {
		scannerSensors[i] = scanner.SensorConfig{
			Name:       sensor.Name,
			ID:         sensor.ID,
			MACAddress: sensor.MACAddress,
		}
	}

	// Start BLE scanner in goroutine (writes to BOTH buffers)
	bleScanner := scanner.New(scannerSensors, metricsBuffer, controlBuffer, logger)
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := bleScanner.Start(ctx)
		if err != nil {
			logger.Error("BLE scanner failed", zap.Error(err))
			cancel() // Cancel context to stop other goroutines
		}
	}()

	// Add Power poller job if enabled
	if cfg.Power.Enabled {
		logger.Info("power monitoring enabled, adding scheduler job")

		powerScraper := power.New(
			cfg.Power.ScrapeURL,
			time.Duration(cfg.Power.ScrapeTimeoutSeconds*float64(time.Second)),
			logger,
		)

		powerPoller := power.NewPoller(
			powerScraper,
			metricsBuffer,
			logger,
		)

		if err := jobScheduler.AddCronJobWithSeconds("Power Meter Poller", cfg.Power.Cron, powerPoller.Run); err != nil {
			logger.Error("failed to add power poller cron job", zap.Error(err))
			os.Exit(1)
		}
	} else {
		logger.Info("power monitoring disabled")
	}

	// Add BLE aggregator job if enabled
	if cfg.Aggregator.Enabled {
		logger.Info("BLE aggregator enabled, adding scheduler job")

		bleAggregator := aggregator.New(
			scannerSensors,
			controlBuffer,
			metricsBuffer,
			logger,
		)

		if err := jobScheduler.AddCronJobWithSeconds("BLE Aggregator", cfg.Aggregator.Cron, bleAggregator.Run); err != nil {
			logger.Error("failed to add BLE aggregator cron job", zap.Error(err))
			os.Exit(1)
		}
	} else {
		logger.Info("BLE aggregator disabled")
	}

	// Add Prometheus pusher job (runs independently)
	if cfg.Prometheus.Cron != "" {
		if err := jobScheduler.AddCronJobWithSeconds("Prometheus Pusher", cfg.Prometheus.Cron, pusher.Run); err != nil {
			logger.Error("failed to add prometheus pusher cron job", zap.Error(err))
			os.Exit(1)
		}
	} else {
		// Use random duration: interval ± 1 second to prevent thundering herd
		baseInterval := time.Duration(cfg.Prometheus.PushIntervalSeconds) * time.Second
		minInterval := baseInterval - time.Second
		maxInterval := baseInterval + time.Second

		// Ensure minimum interval is at least 1 second
		if minInterval < time.Second {
			minInterval = time.Second
		}

		if err := jobScheduler.AddJobWithRandomDuration(
			"Prometheus Pusher",
			minInterval,
			maxInterval,
			pusher.Run,
		); err != nil {
			logger.Error("failed to add prometheus pusher job", zap.Error(err))
			os.Exit(1)
		}
	}

	// Start the job scheduler
	if err := jobScheduler.Start(); err != nil {
		logger.Error("failed to start job scheduler", zap.Error(err))
		os.Exit(1)
	}

	// Wait for shutdown signal
	select {
	case sig := <-sigChan:
		logger.Info("received shutdown signal", zap.String("signal", sig.String()))
	case <-ctx.Done():
		logger.Info("context cancelled")
	}

	// Cancel context to stop all goroutines
	cancel()

	// Stop scanner
	logger.Info("stopping BLE scanner")
	if err := bleScanner.Stop(); err != nil {
		logger.Error("failed to stop BLE scanner", zap.Error(err))
	}

	// Shutdown scheduler
	logger.Info("shutting down job scheduler")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := jobScheduler.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown job scheduler", zap.Error(err))
	}

	// Final push of remaining data from metrics buffer
	logger.Info("performing final metrics push")
	readings := metricsBuffer.GetAll()
	if len(readings) > 0 {
		finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer finalCancel()

		err := pusher.Push(finalCtx, readings)
		if err != nil {
			logger.Error("failed final metrics push", zap.Error(err))
		} else {
			logger.Info("final metrics push successful", zap.Int("reading_count", len(readings)))
		}
	}

	// Wait for all goroutines to finish
	logger.Info("waiting for goroutines to finish")
	wg.Wait()

	logger.Info("BLE temperature monitoring service stopped")
}
