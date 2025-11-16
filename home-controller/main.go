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
	"github.com/mjasion/balena-home/thermostats/control"
	"github.com/mjasion/balena-home/thermostats/metrics"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	homeOtel "github.com/mjasion/balena-home/thermostats/otel"
	"github.com/mjasion/balena-home/thermostats/power"
	"github.com/mjasion/balena-home/thermostats/pyroscope"
	"github.com/mjasion/balena-home/thermostats/scanner"
	"github.com/mjasion/balena-home/thermostats/scheduler"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
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

	// Initialize OpenTelemetry tracer if enabled
	shutdownTracer, err := homeOtel.InitTracer(context.Background(), &cfg.OpenTelemetry, logger)
	if err != nil {
		logger.Error("failed to initialize opentelemetry tracer", zap.Error(err))
		os.Exit(1)
	}
	defer func() {
		if err := shutdownTracer(context.Background()); err != nil {
			logger.Error("failed to shutdown opentelemetry tracer", zap.Error(err))
		}
	}()

	// Create dual ring buffers for separate purposes
	// Metrics buffer: Used by metrics pusher (cleared every push interval)
	metricsBuffer := buffer.New(cfg.Prometheus.BufferSize, logger)
	logger.Info("metrics buffer created", zap.Int("capacity", cfg.Prometheus.BufferSize))

	// Control buffer: Used by control loop (auto-cleanup enabled, keeps last 5 minutes)
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

	// Create Netatmo client if thermostat control is enabled
	// Controller handles both control and metrics collection
	var netatmoClient *netatmo.Client
	if cfg.ThermostatControl.Enabled {
		logger.Info("creating Netatmo API client for thermostat control")
		netatmoClient = netatmo.NewClient(
			cfg.Netatmo.ClientID,
			cfg.Netatmo.ClientSecret,
			cfg.Netatmo.RefreshToken,
		)
		netatmoClient.SetLogger(logger)
	}

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

	// Initialize and add thermostat control jobs if enabled
	if cfg.ThermostatControl.Enabled {
		logger.Info("thermostat control enabled, initializing and adding scheduler jobs")

		// Create controller (uses shared Netatmo client)
		controller := control.New(
			&cfg.ThermostatControl,
			netatmoClient,
			controlBuffer,
			metricsBuffer,
			logger,
		)

		// Initialize controller (fetch room IDs from Netatmo)
		if err := controller.Initialize(ctx); err != nil {
			logger.Error("failed to initialize thermostat controller", zap.Error(err))
			os.Exit(1)
		}

		// Create home status fetcher
		// Get tracer for home status fetcher
		tracer := otel.Tracer("home-controller/control")
		homeStatusFetcher := control.NewHomeStatusFetcher(controller, pusher, logger, tracer)

		// Add home status fetch job
		// This job fetches home status, stores it in cache, adds to metrics buffer, and pushes metrics
		if err := jobScheduler.AddCronJobWithSeconds("Home Status Fetcher", cfg.ThermostatControl.HomeStatusFetchCron, homeStatusFetcher.Run); err != nil {
			logger.Error("failed to add home status fetcher cron job", zap.Error(err))
			os.Exit(1)
		}

		// Add thermostat control job
		// This job uses cached home status from the fetch job
		if err := jobScheduler.AddCronJobWithSeconds("Thermostat Controller", cfg.ThermostatControl.ControlLoopCron, controller.Run); err != nil {
			logger.Error("failed to add thermostat controller cron job", zap.Error(err))
			os.Exit(1)
		}

		logger.Info("thermostat control jobs configured",
			zap.String("home_status_cron", cfg.ThermostatControl.HomeStatusFetchCron),
			zap.String("control_cron", cfg.ThermostatControl.ControlLoopCron),
		)
	} else {
		logger.Info("thermostat control disabled")

		// Add standalone Prometheus pusher job if thermostat control is disabled
		// When thermostat control is enabled, metrics are pushed by the home status fetcher
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
