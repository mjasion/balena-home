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

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/metrics"
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

	// Create ring buffer
	ringBuffer := buffer.New(cfg.Prometheus.BufferSize, logger)
	logger.Info("ring buffer created", zap.Int("capacity", cfg.Prometheus.BufferSize))

	// Create Prometheus pusher
	pusher := metrics.New(
		cfg.Prometheus.URL,
		cfg.Prometheus.Username,
		cfg.Prometheus.Password,
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

	// Start BLE scanner in goroutine
	bleScanner := scanner.New(scannerSensors, ringBuffer, logger)
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := bleScanner.Start(ctx)
		if err != nil {
			logger.Error("BLE scanner failed", zap.Error(err))
			cancel() // Cancel context to stop other goroutines
		}
	}()

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

	// Start Prometheus pusher goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Duration(cfg.Prometheus.PushIntervalSeconds) * time.Second)
		defer ticker.Stop()

		logger.Info("prometheus pusher started",
			zap.Int("push_interval_seconds", cfg.Prometheus.PushIntervalSeconds),
		)

		for {
			select {
			case <-ctx.Done():
				logger.Info("prometheus pusher stopping")
				return
			case <-ticker.C:
				// Get all readings from buffer
				readings := ringBuffer.GetAll()
				if len(readings) > 0 {
					logger.Debug("pushing metrics to prometheus",
						zap.Int("reading_count", len(readings)),
					)

					err := pusher.Push(ctx, readings)
					if err != nil {
						logger.Error("failed to push metrics",
							zap.Error(err),
							zap.Int("reading_count", len(readings)),
						)
					} else {
						// Clear buffer after successful push
						ringBuffer.Clear()
						logger.Debug("buffer cleared after successful push")
					}
				} else {
					logger.Debug("no readings to push")
				}
			}
		}
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

	// Final push of remaining data
	logger.Info("performing final metrics push")
	readings := ringBuffer.GetAll()
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
