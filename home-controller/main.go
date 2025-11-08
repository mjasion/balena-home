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
	"github.com/mjasion/balena-home/thermostats/power"
	"github.com/mjasion/balena-home/thermostats/pyroscope"
	"github.com/mjasion/balena-home/thermostats/scanner"
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

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create wait group for goroutines
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

	// Create shared Netatmo client if needed (for both poller and control loop)
	// This prevents OAuth token conflicts when multiple components use the same credentials
	var netatmoClient *netatmo.Client
	if cfg.Netatmo.Enabled || cfg.ThermostatControl.Enabled {
		logger.Info("creating shared Netatmo API client")
		netatmoClient = netatmo.NewClient(
			cfg.Netatmo.ClientID,
			cfg.Netatmo.ClientSecret,
			cfg.Netatmo.RefreshToken,
		)
	}

	// Start Netatmo poller if enabled (writes to metrics buffer only)
	if cfg.Netatmo.Enabled {
		logger.Info("netatmo integration enabled, starting poller")

		netatmoPoller := netatmo.NewPoller(
			netatmoClient,
			metricsBuffer,
			cfg.Netatmo.FetchInterval,
			logger,
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			netatmoPoller.Start(ctx)
		}()
	} else {
		logger.Info("netatmo integration disabled")
	}

	// Start Power poller if enabled (writes to metrics buffer only)
	if cfg.Power.Enabled {
		logger.Info("power monitoring enabled, starting poller")

		powerScraper := power.New(
			cfg.Power.ScrapeURL,
			time.Duration(cfg.Power.ScrapeTimeoutSeconds*float64(time.Second)),
			logger,
		)

		powerPoller := power.NewPoller(
			powerScraper,
			metricsBuffer,
			cfg.Power.ScrapeIntervalSeconds,
			logger,
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			powerPoller.Start(ctx)
		}()
	} else {
		logger.Info("power monitoring disabled")
	}

	// Start BLE aggregator if enabled (calculates weighted averages)
	if cfg.Aggregator.Enabled {
		logger.Info("BLE aggregator enabled, starting aggregator")

		bleAggregator := aggregator.New(
			scannerSensors,
			controlBuffer,
			metricsBuffer,
			cfg.Aggregator.IntervalSeconds,
			logger,
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := bleAggregator.Start(ctx)
			if err != nil {
				logger.Error("BLE aggregator failed", zap.Error(err))
			}
		}()
	} else {
		logger.Info("BLE aggregator disabled")
	}

	// Start thermostat control loop if enabled (uses control buffer)
	if cfg.ThermostatControl.Enabled {
		logger.Info("thermostat control enabled, starting control loop")

		// Create controller (uses shared Netatmo client)
		controller := control.New(
			&cfg.ThermostatControl,
			netatmoClient,
			controlBuffer,
			metricsBuffer,
			logger,
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := controller.Start(ctx)
			if err != nil {
				logger.Error("thermostat control loop failed", zap.Error(err))
			}
		}()
	} else {
		logger.Info("thermostat control disabled")
	}

	// Wait for START_AT_EVEN_SECOND if configured
	if cfg.Prometheus.StartAtEvenSecond {
		now := time.Now()
		nextEvenSecond := now.Truncate(time.Second).Add(time.Second)
		waitDuration := nextEvenSecond.Sub(now)
		logger.Info("waiting to start at even second",
			zap.Duration("wait_duration", waitDuration),
			zap.Time("next_even_second", nextEvenSecond),
		)
		time.Sleep(waitDuration)
	}

	// Start Prometheus pusher
	wg.Add(1)
	go func() {
		defer wg.Done()
		pusher.Start(ctx)
	}()

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
