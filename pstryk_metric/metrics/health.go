package metrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mjasion/balena-home/pstryk_metric/buffer"
	"go.uber.org/zap"
)

// HealthStatus represents the health status of the service
type HealthStatus struct {
	Status          string    `json:"status"`
	LastScrapeTime  time.Time `json:"lastScrapeTime"`
	LastPushTime    time.Time `json:"lastPushTime"`
	BufferedSamples int       `json:"bufferedSamples"`
}

// HealthChecker provides health check functionality
type HealthChecker struct {
	buffer            *buffer.RingBuffer
	pusher            *Pusher
	scrapeInterval    time.Duration
	healthCheckServer *http.Server
	logger            *zap.Logger
}

// NewHealthChecker creates a new HealthChecker instance
func NewHealthChecker(buf *buffer.RingBuffer, pusher *Pusher, scrapeInterval time.Duration, port int, logger *zap.Logger) *HealthChecker {
	hc := &HealthChecker{
		buffer:         buf,
		pusher:         pusher,
		scrapeInterval: scrapeInterval,
		logger:         logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", hc.handleHealth)

	hc.healthCheckServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	return hc
}

// Start begins serving the health check endpoint
func (hc *HealthChecker) Start() error {
	hc.logger.Info("Starting health check server", zap.String("addr", hc.healthCheckServer.Addr))
	if err := hc.healthCheckServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("health check server error: %w", err)
	}
	return nil
}

// Stop gracefully shuts down the health check server
func (hc *HealthChecker) Stop() error {
	return hc.healthCheckServer.Close()
}

// handleHealth responds to health check requests
func (hc *HealthChecker) handleHealth(w http.ResponseWriter, r *http.Request) {
	lastScrape := hc.buffer.GetLastScrapeTime()
	lastPush := hc.pusher.GetLastPushTime()
	bufferedSamples := hc.buffer.Size()

	status := HealthStatus{
		Status:          "healthy",
		LastScrapeTime:  lastScrape,
		LastPushTime:    lastPush,
		BufferedSamples: bufferedSamples,
	}

	// Check if scraping is stale (more than 2x the scrape interval)
	if !lastScrape.IsZero() && time.Since(lastScrape) > 2*hc.scrapeInterval {
		status.Status = "unhealthy"
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
