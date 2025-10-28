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
	"github.com/mjasion/balena-home/thermostats/netatmo"
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
		ringBuffer,
		cfg.Prometheus.PushIntervalSeconds,
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

	// Start Netatmo poller if enabled
	if cfg.Netatmo.Enabled {
		logger.Info("netatmo integration enabled, starting poller")

		netatmoFetcher := netatmo.NewFetcher(
			cfg.Netatmo.ClientID,
			cfg.Netatmo.ClientSecret,
			cfg.Netatmo.RefreshToken,
		)

		netatmoPoller := netatmo.NewPoller(
			netatmoFetcher,
			ringBuffer,
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
