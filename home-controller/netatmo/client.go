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
	baseURL           = "https://api.netatmo.com"
	tokenURL          = "https://api.netatmo.com/oauth2/token"
	homesDataURL      = "https://api.netatmo.com/api/homesdata"
	homeStatusURL     = "https://api.netatmo.com/api/homestatus"
	setThermPointURL  = "https://api.netatmo.com/api/setroomthermpoint"
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
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", c.refreshToken)
	data.Set("client_id", c.clientID)
	data.Set("client_secret", c.clientSecret)

	tokenURLToUse := tokenURL
	if c.tokenURLOverride != "" {
		tokenURLToUse = c.tokenURLOverride
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURLToUse, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode token response: %w", err)
	}

	c.accessToken = tokenResp.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	// Update refresh token if a new one is provided
	if tokenResp.RefreshToken != "" {
		c.refreshToken = tokenResp.RefreshToken
	}

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
func (c *Client) doRequest(ctx context.Context, method, requestURL string, body io.Reader, result interface{}) error {
	tracer := otel.Tracer("netatmo")

	// Parse URL to extract endpoint information
	parsedURL, _ := url.Parse(requestURL)
	endpoint := "unknown"
	if parsedURL != nil && parsedURL.Path != "" {
		// Extract last segment of path as endpoint name
		pathParts := strings.Split(strings.TrimPrefix(parsedURL.Path, "/api/"), "/")
		if len(pathParts) > 0 {
			endpoint = pathParts[0]
		}
	}

	ctx, span := tracer.Start(ctx, "netatmo.api_request",
		trace.WithAttributes(
			attribute.String("http.method", method),
			attribute.String("http.url", requestURL),
			attribute.String("netatmo.endpoint", endpoint),
		),
	)
	defer span.End()

	// Add additional URL components as attributes
	if parsedURL != nil {
		span.SetAttributes(
			attribute.String("http.scheme", parsedURL.Scheme),
			attribute.String("http.host", parsedURL.Host),
			attribute.String("http.target", parsedURL.Path),
		)
		if parsedURL.RawQuery != "" {
			span.SetAttributes(attribute.String("http.query", parsedURL.RawQuery))
		}
	}

	if err := c.ensureToken(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to ensure token")
		return fmt.Errorf("failed to ensure token: %w", err)
	}

	// Read request body once for retries and tracing
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to read request body")
			return fmt.Errorf("failed to read request body: %w", err)
		}
		// Add request body to span (for POST requests with form data)
		if len(bodyBytes) > 0 {
			span.SetAttributes(attribute.String("http.request.body", string(bodyBytes)))
		}
	}

	// Apply rate limiting
	c.rateLimit()

	// Retry with exponential backoff for 429 rate limit errors
	maxRetries := 3
	baseDelay := 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		span.AddEvent("attempt", trace.WithAttributes(
			attribute.Int("attempt_number", attempt+1),
			attribute.Int("max_retries", maxRetries+1),
		))

		// Create request body reader for this attempt
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		// Create request (recreate for each retry)
		req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create request")
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.accessToken)
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "request failed")
			return fmt.Errorf("request failed: %w", err)
		}

		span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

		// Read full response body for logging
		respBodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Check status code
		if resp.StatusCode == http.StatusOK {
			// Log full response at debug level
			c.logger.Debug("Netatmo API response",
				zap.String("url", requestURL),
				zap.String("method", method),
				zap.Int("status_code", resp.StatusCode),
				zap.String("response_body", string(respBodyBytes)),
			)

			// Add response body to span
			span.SetAttributes(attribute.String("http.response.body", string(respBodyBytes)))

			// Success - decode and return
			if result != nil {
				if err := json.Unmarshal(respBodyBytes, result); err != nil {
					span.RecordError(err)
					span.SetStatus(codes.Error, "failed to decode response")
					return fmt.Errorf("failed to decode response: %w", err)
				}
			}
			span.SetStatus(codes.Ok, "success")
			return nil
		}

		errMsg := string(respBodyBytes)

		// Log error response
		c.logger.Warn("Netatmo API error response",
			zap.String("url", requestURL),
			zap.String("method", method),
			zap.Int("status_code", resp.StatusCode),
			zap.String("response_body", errMsg),
		)

		span.SetAttributes(attribute.String("http.response.body", errMsg))

		// Check if it's a 429 rate limit error
		if resp.StatusCode == 429 || strings.Contains(errMsg, "concurrency limited") {
			if attempt < maxRetries {
				// Calculate exponential backoff: 2s, 4s, 8s
				delay := baseDelay * time.Duration(1<<uint(attempt))
				c.logger.Warn("Netatmo API rate limit hit, retrying with backoff",
					zap.String("url", requestURL),
					zap.Int("attempt", attempt+1),
					zap.Int("max_retries", maxRetries+1),
					zap.Duration("backoff_delay", delay),
				)

				span.AddEvent("rate_limit_backoff", trace.WithAttributes(
					attribute.Int64("backoff_delay_ms", delay.Milliseconds()),
				))

				select {
				case <-time.After(delay):
					// Continue to next attempt
					continue
				case <-ctx.Done():
					err := fmt.Errorf("context cancelled during rate limit backoff: %w", ctx.Err())
					span.RecordError(err)
					span.SetStatus(codes.Error, "context cancelled")
					return err
				}
			} else {
				// Max retries exhausted
				c.logger.Error("max retries exhausted for Netatmo API call",
					zap.String("url", requestURL),
					zap.Int("attempts", attempt+1),
				)
			}
		}

		// Not a rate limit error or max retries exhausted
		err = fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, errMsg)
		span.RecordError(err)
		span.SetStatus(codes.Error, fmt.Sprintf("status %d", resp.StatusCode))
		return err
	}

	err := fmt.Errorf("API request failed after %d retries", maxRetries+1)
	span.RecordError(err)
	span.SetStatus(codes.Error, "max retries exhausted")
	return err
}

// GetHomesData retrieves homes data including topology
func (c *Client) GetHomesData(ctx context.Context) (*HomesDataResponse, error) {
	var response HomesDataResponse
	if err := c.doRequest(ctx, "GET", homesDataURL, nil, &response); err != nil {
		return nil, fmt.Errorf("failed to get homes data: %w", err)
	}
	return &response, nil
}

// GetHomeStatus retrieves the current status of a specific home
func (c *Client) GetHomeStatus(ctx context.Context, homeID string) (*HomeStatusResponse, error) {
	requestURL := fmt.Sprintf("%s?home_id=%s", homeStatusURL, url.QueryEscape(homeID))

	var response HomeStatusResponse
	if err := c.doRequest(ctx, "GET", requestURL, nil, &response); err != nil {
		return nil, fmt.Errorf("failed to get home status: %w", err)
	}
	return &response, nil
}

// SetRoomThermpoint sets the temperature setpoint for a specific room
// mode: "manual" (set specific temperature) or "home" (return to schedule)
// temp: target temperature in Celsius (required for "manual" mode, ignored for "home" mode)
// endTime: optional Unix timestamp for temporary override (0 for permanent until next schedule change)
func (c *Client) SetRoomThermpoint(ctx context.Context, homeID, roomID string, mode string, temp float64, endTime int64) error {
	// Validate mode
	if mode != "manual" && mode != "home" {
		return fmt.Errorf("invalid mode: %s (must be 'manual' or 'home')", mode)
	}

	// Validate temperature for manual mode
	if mode == "manual" {
		if temp < 7.0 || temp > 30.0 {
			return fmt.Errorf("temperature %.1f°C out of range (must be between 7.0 and 30.0)", temp)
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

	var response SetRoomThermPointResponse
	if err := c.doRequest(ctx, "POST", setThermPointURL, bytes.NewBufferString(data.Encode()), &response); err != nil {
		return fmt.Errorf("failed to set room thermpoint: %w", err)
	}

	// Check response status
	if response.Status != "ok" {
		return fmt.Errorf("API returned non-ok status: %s", response.Status)
	}

	return nil
}

// FetchAllThermostats fetches thermostat data from all homes and rooms
func (c *Client) FetchAllThermostats(ctx context.Context) ([]ThermostatReading, error) {
	// First, get homes data to know the topology
	homesData, err := c.GetHomesData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get homes data: %w", err)
	}

	if homesData.Status != "ok" {
		return nil, fmt.Errorf("homes data request returned status: %s", homesData.Status)
	}

	var readings []ThermostatReading

	// For each home, get the current status
	for _, home := range homesData.Body.Homes {
		homeStatus, err := c.GetHomeStatus(ctx, home.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get status for home %s: %w", home.Name, err)
		}

		if homeStatus.Status != "ok" {
			return nil, fmt.Errorf("home status request for %s returned status: %s", home.Name, homeStatus.Status)
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

	return readings, nil
}
