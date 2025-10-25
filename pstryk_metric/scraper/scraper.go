package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Scraper handles fetching and parsing energy meter data
type Scraper struct {
	client  *http.Client
	url     string
	timeout time.Duration
	logger  *zap.Logger
}

// New creates a new Scraper instance
func New(url string, timeout time.Duration, logger *zap.Logger) *Scraper {
	return &Scraper{
		client: &http.Client{
			Timeout: timeout,
		},
		url:    url,
		logger: logger,
	}
}

// Scrape fetches data from the energy meter and extracts active power readings
func (s *Scraper) Scrape(ctx context.Context) (*ScrapeResult, error) {
	result := &ScrapeResult{
		Timestamp: time.Now(),
	}

	// Try up to 3 times with exponential backoff
	var lastErr error
	backoff := time.Second

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
			s.logger.Info("Retrying scrape", zap.Int("attempt", attempt+1), zap.Int("maxAttempts", 3))
		}

		readings, err := s.scrapeOnce(ctx)
		if err == nil {
			result.Readings = readings
			return result, nil
		}

		lastErr = err
		s.logger.Warn("Scrape attempt failed",
			zap.Int("attempt", attempt+1),
			zap.Error(err))
	}

	result.Error = fmt.Errorf("all retry attempts exhausted: %w", lastErr)
	return result, result.Error
}

// scrapeOnce performs a single scrape attempt
func (s *Scraper) scrapeOnce(ctx context.Context) ([]ActivePowerReading, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var data MultiSensorResponse
	if err := json.Unmarshal(body, &data); err != nil {
		// Log a sample of the response for debugging
		sample := string(body)
		if len(sample) > 200 {
			sample = sample[:200] + "..."
		}
		s.logger.Error("Failed to parse JSON",
			zap.String("sample", sample),
			zap.Error(err))
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	readings := data.FilterActivePower()
	if len(readings) == 0 {
		s.logger.Warn("No activePower sensors found in response")
	}

	return readings, nil
}
