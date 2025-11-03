package netatmo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	baseURL           = "https://api.netatmo.com"
	tokenURL          = "https://api.netatmo.com/oauth2/token"
	homesDataURL      = "https://api.netatmo.com/api/homesdata"
	homeStatusURL     = "https://api.netatmo.com/api/homestatus"
	setThermPointURL  = "https://api.netatmo.com/api/setroomthermpoint"
)

// Client represents a Netatmo API client
type Client struct {
	httpClient       *http.Client
	clientID         string
	clientSecret     string
	refreshToken     string
	accessToken      string
	tokenExpiry      time.Time
	tokenURLOverride string // For testing only
}

// NewClient creates a new Netatmo API client
func NewClient(clientID, clientSecret, refreshToken string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		clientID:     clientID,
		clientSecret: clientSecret,
		refreshToken: refreshToken,
	}
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

// doRequest performs an authenticated API request
func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader, result interface{}) error {
	if err := c.ensureToken(ctx); err != nil {
		return fmt.Errorf("failed to ensure token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
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
