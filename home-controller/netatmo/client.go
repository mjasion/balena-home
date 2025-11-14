package netatmo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	baseURL          = "https://api.netatmo.com"
	tokenURL         = "https://api.netatmo.com/oauth2/token"
	homesDataURL     = "https://api.netatmo.com/api/homesdata"
	homeStatusURL    = "https://api.netatmo.com/api/homestatus"
	setThermPointURL = "https://api.netatmo.com/api/setroomthermpoint"
)

// Client represents a Netatmo API client with built-in rate limiting and retry logic
type Client struct {
	httpClient       *http.Client
	clientID         string
	clientSecret     string
	refreshToken     string
	accessToken      string
	tokenExpiry      time.Time
	tokenURLOverride string // For testing only

	// Rate limiting (global for all API calls)
	rateLimiter *time.Ticker
	rateMu      sync.Mutex
	lastAPICall time.Time

	// Logger for retry warnings
	logger *zap.Logger

	// Tracer for OpenTelemetry spans
	tracer trace.Tracer
}

// NewClient creates a new Netatmo API client with rate limiting
func NewClient(clientID, clientSecret, refreshToken string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		clientID:     clientID,
		clientSecret: clientSecret,
		refreshToken: refreshToken,
		logger:       zap.NewNop(), // Default no-op logger, can be set via SetLogger
		tracer:       otel.Tracer("home-controller/netatmo"),
	}
}

// SetLogger sets the logger for the client (for retry warnings)
func (c *Client) SetLogger(logger *zap.Logger) {
	c.logger = logger
}

// tokenResponse represents the OAuth2 token response
type tokenResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int      `json:"expires_in"`
	Scope        []string `json:"scope"`
}

// refreshAccessToken refreshes the OAuth2 access token
func (c *Client) refreshAccessToken(ctx context.Context) error {
	ctx, span := c.tracer.Start(ctx, "netatmo.refreshAccessToken",
		trace.WithAttributes(
			attribute.String("client_id", c.clientID),
		),
	)
	defer span.End()

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", c.refreshToken)
	data.Set("client_id", c.clientID)
	data.Set("client_secret", c.clientSecret)

	tokenURLToUse := tokenURL
	if c.tokenURLOverride != "" {
		tokenURLToUse = c.tokenURLOverride
	}

	c.logger.Debug("refreshing Netatmo access token",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("token_url", tokenURLToUse),
		zap.String("grant_type", "refresh_token"),
	)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURLToUse, bytes.NewBufferString(data.Encode()))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create token request")
		return fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to refresh token")
		return fmt.Errorf("failed to refresh token: %w", err)
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		c.logger.Debug("token refresh failed",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", string(body)),
		)

		// Add error response body to span
		span.SetAttributes(attribute.String("http.response.body", string(body)))
		span.SetStatus(codes.Error, "token refresh failed")
		return fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to decode token response")
		return fmt.Errorf("failed to decode token response: %w", err)
	}

	c.accessToken = tokenResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	// Update refresh token if a new one is provided
	if tokenResp.RefreshToken != "" {
		c.refreshToken = tokenResp.RefreshToken
	}

	c.logger.Debug("token refreshed successfully",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("expires_in", tokenResp.ExpiresIn),
		zap.Time("token_expiry", c.tokenExpiry),
	)

	span.SetAttributes(
		attribute.Int("expires_in", tokenResp.ExpiresIn),
		attribute.Bool("refresh_token_updated", tokenResp.RefreshToken != ""),
	)
	span.SetStatus(codes.Ok, "token refreshed successfully")

	return nil
}

// ensureToken ensures we have a valid access token
func (c *Client) ensureToken(ctx context.Context) error {
	// Refresh if token is expired or about to expire (within 5 minutes)
	if c.accessToken == "" || time.Until(c.tokenExpiry) < 5*time.Minute {
		return c.refreshAccessToken(ctx)
	}
	return nil
}

// rateLimit enforces a minimum delay between API calls (thread-safe)
func (c *Client) rateLimit() {
	c.rateMu.Lock()
	defer c.rateMu.Unlock()

	// Enforce minimum 1 second between API calls
	minDelay := 1 * time.Second
	elapsed := time.Since(c.lastAPICall)
	if elapsed < minDelay {
		time.Sleep(minDelay - elapsed)
	}
	c.lastAPICall = time.Now()
}

// doRequest performs an authenticated API request with exponential backoff for rate limits
func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader, result interface{}) error {
	ctx, span := c.tracer.Start(ctx, "netatmo.doRequest",
		trace.WithAttributes(
			attribute.String("http.method", method),
			attribute.String("http.url", url),
		),
	)
	defer span.End()

	// Read body for logging if present
	var bodyStr string
	if body != nil {
		if buf, ok := body.(*bytes.Buffer); ok {
			bodyStr = buf.String()
			// Recreate buffer since we read it
			body = bytes.NewBufferString(bodyStr)
		}
	}

	c.logger.Debug("Netatmo API request",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("method", method),
		zap.String("url", url),
		zap.String("body", bodyStr),
	)

	if err := c.ensureToken(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to ensure token")
		return fmt.Errorf("failed to ensure token: %w", err)
	}

	// Apply rate limiting
	c.rateLimit()

	// Retry with exponential backoff for 429 rate limit errors
	maxRetries := 3
	baseDelay := 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		span.AddEvent(fmt.Sprintf("attempt_%d", attempt+1))

		// Create request (need to recreate for each retry if body is involved)
		var req *http.Request
		var err error
		if bodyStr != "" {
			req, err = http.NewRequestWithContext(ctx, method, url, bytes.NewBufferString(bodyStr))
		} else {
			req, err = http.NewRequestWithContext(ctx, method, url, nil)
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create request")
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.accessToken)
		if bodyStr != "" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "request failed")
			return fmt.Errorf("request failed: %w", err)
		}

		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

		// Check status code
		if resp.StatusCode == http.StatusOK {
			// Success - decode and return
			if result != nil {
				bodyBytes, _ := io.ReadAll(resp.Body)
				c.logger.Debug("Netatmo API response",
					zap.String("trace_id", span.SpanContext().TraceID().String()),
					zap.String("url", url),
					zap.Int("status_code", resp.StatusCode),
					zap.String("response_body", string(bodyBytes)),
				)

				// Add response body to span
				span.SetAttributes(attribute.String("http.response.body", string(bodyBytes)))

				if err := json.Unmarshal(bodyBytes, result); err != nil {
					resp.Body.Close()
					span.RecordError(err)
					span.SetStatus(codes.Error, "failed to decode response")
					return fmt.Errorf("failed to decode response: %w", err)
				}
			}
			resp.Body.Close()
			span.SetStatus(codes.Ok, "request successful")
			return nil
		}

		// Read error body
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		errMsg := string(bodyBytes)

		c.logger.Debug("Netatmo API error response",
			zap.String("trace_id", span.SpanContext().TraceID().String()),
			zap.String("url", url),
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", errMsg),
			zap.Int("attempt", attempt+1),
		)

		// Add error response body to span
		span.SetAttributes(attribute.String("http.response.body", errMsg))

		// Check if it's a 429 rate limit error
		if resp.StatusCode == 429 || strings.Contains(errMsg, "concurrency limited") {
			span.AddEvent("rate_limit_hit", trace.WithAttributes(
				attribute.Int("attempt", attempt+1),
				attribute.Int("status_code", resp.StatusCode),
			))

			if attempt < maxRetries {
				// Calculate exponential backoff: 2s, 4s, 8s
				delay := baseDelay * time.Duration(1<<uint(attempt))
				c.logger.Warn("Netatmo API rate limit hit, retrying with backoff",
					zap.String("trace_id", span.SpanContext().TraceID().String()),
					zap.String("url", url),
					zap.Int("attempt", attempt+1),
					zap.Int("max_retries", maxRetries+1),
					zap.Duration("backoff_delay", delay),
				)

				select {
				case <-time.After(delay):
					// Continue to next attempt
					continue
				case <-ctx.Done():
					span.RecordError(ctx.Err())
					span.SetStatus(codes.Error, "context cancelled during rate limit backoff")
					return fmt.Errorf("context cancelled during rate limit backoff: %w", ctx.Err())
				}
			} else {
				// Max retries exhausted
				c.logger.Error("max retries exhausted for Netatmo API call",
					zap.String("trace_id", span.SpanContext().TraceID().String()),
					zap.String("url", url),
					zap.Int("attempts", attempt+1),
				)
			}
		}

		// Not a rate limit error or max retries exhausted
		span.SetStatus(codes.Error, fmt.Sprintf("API request failed with status %d", resp.StatusCode))
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, errMsg)
	}

	span.SetAttributes(attribute.Int("total_attempts", maxRetries+1))
	span.SetStatus(codes.Error, "max retries exhausted")
	return fmt.Errorf("API request failed after %d retries", maxRetries+1)
}

// GetHomesData retrieves homes data including topology
func (c *Client) GetHomesData(ctx context.Context) (*HomesDataResponse, error) {
	ctx, span := c.tracer.Start(ctx, "netatmo.GetHomesData")
	defer span.End()

	var response HomesDataResponse
	if err := c.doRequest(ctx, "GET", homesDataURL, nil, &response); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get homes data")
		return nil, fmt.Errorf("failed to get homes data: %w", err)
	}

	span.SetAttributes(
		attribute.Int("homes_count", len(response.Body.Homes)),
		attribute.String("status", response.Status),
	)
	span.SetStatus(codes.Ok, "homes data retrieved successfully")

	return &response, nil
}

// GetHomeStatus retrieves the current status of a specific home
func (c *Client) GetHomeStatus(ctx context.Context, homeID string) (*HomeStatusResponse, error) {
	ctx, span := c.tracer.Start(ctx, "netatmo.GetHomeStatus",
		trace.WithAttributes(
			attribute.String("home_id", homeID),
		),
	)
	defer span.End()

	requestURL := fmt.Sprintf("%s?home_id=%s", homeStatusURL, url.QueryEscape(homeID))

	var response HomeStatusResponse
	if err := c.doRequest(ctx, "GET", requestURL, nil, &response); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get home status")
		return nil, fmt.Errorf("failed to get home status: %w", err)
	}

	span.SetAttributes(
		attribute.Int("rooms_count", len(response.Body.Home.Rooms)),
		attribute.String("status", response.Status),
	)
	span.SetStatus(codes.Ok, "home status retrieved successfully")

	return &response, nil
}

// SetRoomThermpoint sets the temperature setpoint for a specific room
// mode: "manual" (set specific temperature) or "home" (return to schedule)
// temp: target temperature in Celsius (required for "manual" mode, ignored for "home" mode)
// endTime: optional Unix timestamp for temporary override (0 for permanent until next schedule change)
func (c *Client) SetRoomThermpoint(ctx context.Context, homeID, roomID string, mode string, temp float64, endTime int64) error {
	ctx, span := c.tracer.Start(ctx, "netatmo.SetRoomThermpoint",
		trace.WithAttributes(
			attribute.String("home_id", homeID),
			attribute.String("room_id", roomID),
			attribute.String("mode", mode),
			attribute.Float64("temperature", temp),
			attribute.Int64("end_time", endTime),
		),
	)
	defer span.End()

	// Validate mode
	if mode != "manual" && mode != "home" {
		err := fmt.Errorf("invalid mode: %s (must be 'manual' or 'home')", mode)
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid mode")
		return err
	}

	// Validate temperature for manual mode
	if mode == "manual" {
		if temp < 7.0 || temp > 30.0 {
			err := fmt.Errorf("temperature %.1f°C out of range (must be between 7.0 and 30.0)", temp)
			span.RecordError(err)
			span.SetStatus(codes.Error, "temperature out of range")
			return err
		}
	}

	// Build request body
	data := url.Values{}
	data.Set("home_id", homeID)
	data.Set("room_id", roomID)
	data.Set("mode", mode)

	if mode == "manual" {
		data.Set("temp", fmt.Sprintf("%.1f", temp))
	}

	if endTime > 0 {
		data.Set("endtime", fmt.Sprintf("%d", endTime))
	}

	c.logger.Debug("setting room thermpoint",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("home_id", homeID),
		zap.String("room_id", roomID),
		zap.String("mode", mode),
		zap.Float64("temperature", temp),
		zap.Int64("end_time", endTime),
	)

	var response SetRoomThermPointResponse
	if err := c.doRequest(ctx, "POST", setThermPointURL, bytes.NewBufferString(data.Encode()), &response); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to set room thermpoint")
		return fmt.Errorf("failed to set room thermpoint: %w", err)
	}

	// Check response status
	if response.Status != "ok" {
		err := fmt.Errorf("API returned non-ok status: %s", response.Status)
		span.RecordError(err)
		span.SetStatus(codes.Error, "non-ok status in response")
		return err
	}

	c.logger.Debug("room thermpoint set successfully",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("home_id", homeID),
		zap.String("room_id", roomID),
		zap.String("response_status", response.Status),
	)

	span.SetAttributes(attribute.String("response_status", response.Status))
	span.SetStatus(codes.Ok, "room thermpoint set successfully")

	return nil
}

// FetchAllThermostats fetches thermostat data from all homes and rooms
func (c *Client) FetchAllThermostats(ctx context.Context) ([]ThermostatReading, error) {
	ctx, span := c.tracer.Start(ctx, "netatmo.FetchAllThermostats")
	defer span.End()

	// First, get homes data to know the topology
	homesData, err := c.GetHomesData(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get homes data")
		return nil, fmt.Errorf("failed to get homes data: %w", err)
	}

	if homesData.Status != "ok" {
		err := fmt.Errorf("homes data request returned status: %s", homesData.Status)
		span.RecordError(err)
		span.SetStatus(codes.Error, "non-ok status in homes data")
		return nil, err
	}

	var readings []ThermostatReading

	// For each home, get the current status
	for _, home := range homesData.Body.Homes {
		homeStatus, err := c.GetHomeStatus(ctx, home.ID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, fmt.Sprintf("failed to get status for home %s", home.Name))
			return nil, fmt.Errorf("failed to get status for home %s: %w", home.Name, err)
		}

		if homeStatus.Status != "ok" {
			err := fmt.Errorf("home status request for %s returned status: %s", home.Name, homeStatus.Status)
			span.RecordError(err)
			span.SetStatus(codes.Error, "non-ok status in home status")
			return nil, err
		}

		// Create a map of room ID to room name from topology
		roomNames := make(map[string]string)
		for _, room := range home.Rooms {
			roomNames[room.ID] = room.Name
		}

		// Process each room's thermostat data
		timestamp := time.Now().Unix()
		for _, roomStatus := range homeStatus.Body.Home.Rooms {
			roomName, ok := roomNames[roomStatus.ID]
			if !ok {
				roomName = roomStatus.ID // Fallback to ID if name not found
			}

			reading := ThermostatReading{
				Timestamp:           timestamp,
				HomeID:              home.ID,
				HomeName:            home.Name,
				RoomID:              roomStatus.ID,
				RoomName:            roomName,
				MeasuredTemperature: roomStatus.ThermMeasuredTemperature,
				SetpointTemperature: roomStatus.ThermSetpointTemperature,
				SetpointMode:        roomStatus.ThermSetpointMode,
				HeatingPowerRequest: roomStatus.HeatingPowerRequest,
				OpenWindow:          roomStatus.OpenWindow,
				Reachable:           roomStatus.Reachable,
			}

			readings = append(readings, reading)
		}
	}

	span.SetAttributes(
		attribute.Int("total_readings", len(readings)),
		attribute.Int("homes_count", len(homesData.Body.Homes)),
	)
	span.SetStatus(codes.Ok, "all thermostats fetched successfully")

	c.logger.Debug("fetched all thermostats",
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.Int("total_readings", len(readings)),
		zap.Int("homes_count", len(homesData.Body.Homes)),
	)

	return readings, nil
}
