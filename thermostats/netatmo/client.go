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

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	baseURL      = "https://api.netatmo.com"
	tokenURL     = "https://api.netatmo.com/oauth2/token"
	homesDataURL = "https://api.netatmo.com/api/homesdata"
	homeStatusURL = "https://api.netatmo.com/api/homestatus"
)

// Client represents a Netatmo API client
type Client struct {
	httpClient   *http.Client
	clientID     string
	clientSecret string
	refreshToken string
	accessToken  string
	tokenExpiry  time.Time
}

// NewClient creates a new Netatmo API client with OpenTelemetry instrumentation
func NewClient(clientID, clientSecret, refreshToken string) *Client {
	// Create HTTP client with OpenTelemetry instrumentation
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
				return fmt.Sprintf("netatmo.%s %s", r.Method, r.URL.Path)
			}),
		),
	}

	return &Client{
		httpClient:   httpClient,
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
	tracer := otel.Tracer("netatmo")
	ctx, span := tracer.Start(ctx, "netatmo.refreshAccessToken",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("netatmo.operation", "refresh_token"),
		),
	)
	defer span.End()

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", c.refreshToken)
	data.Set("client_id", c.clientID)
	data.Set("client_secret", c.clientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, bytes.NewBufferString(data.Encode()))
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
		err := fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
		span.RecordError(err)
		span.SetStatus(codes.Error, "token refresh failed")
		return err
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

	span.SetAttributes(
		attribute.Int("netatmo.token_expires_in_seconds", tokenResp.ExpiresIn),
		attribute.Bool("netatmo.refresh_token_updated", tokenResp.RefreshToken != ""),
	)
	span.SetStatus(codes.Ok, "token refreshed successfully")

	return nil
}

// ensureToken ensures we have a valid access token
func (c *Client) ensureToken(ctx context.Context) error {
	tracer := otel.Tracer("netatmo")
	ctx, span := tracer.Start(ctx, "netatmo.ensureToken")
	defer span.End()

	// Refresh if token is expired or about to expire (within 5 minutes)
	needsRefresh := c.accessToken == "" || time.Until(c.tokenExpiry) < 5*time.Minute
	span.SetAttributes(
		attribute.Bool("netatmo.token_needs_refresh", needsRefresh),
		attribute.Bool("netatmo.has_access_token", c.accessToken != ""),
	)

	if !c.tokenExpiry.IsZero() {
		span.SetAttributes(attribute.String("netatmo.token_expiry", c.tokenExpiry.Format(time.RFC3339)))
	}

	if needsRefresh {
		if err := c.refreshAccessToken(ctx); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to refresh access token")
			return err
		}
	}

	span.SetStatus(codes.Ok, "token is valid")
	return nil
}

// doRequest performs an authenticated API request
func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader, result interface{}) error {
	tracer := otel.Tracer("netatmo")
	ctx, span := tracer.Start(ctx, "netatmo.doRequest",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("http.method", method),
			attribute.String("http.url", url),
		),
	)
	defer span.End()

	if err := c.ensureToken(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to ensure token")
		return fmt.Errorf("failed to ensure token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create request")
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "request failed")
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
		span.RecordError(err)
		span.SetStatus(codes.Error, "API request failed")
		return err
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to decode response")
			return fmt.Errorf("failed to decode response: %w", err)
		}
		span.AddEvent("response decoded successfully")
	}

	span.SetStatus(codes.Ok, "request completed successfully")
	return nil
}

// GetHomesData retrieves homes data including topology
func (c *Client) GetHomesData(ctx context.Context) (*HomesDataResponse, error) {
	tracer := otel.Tracer("netatmo")
	ctx, span := tracer.Start(ctx, "netatmo.GetHomesData",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("netatmo.api", "homesdata"),
		),
	)
	defer span.End()

	var response HomesDataResponse
	if err := c.doRequest(ctx, "GET", homesDataURL, nil, &response); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get homes data")
		return nil, fmt.Errorf("failed to get homes data: %w", err)
	}

	// Add metadata about the response
	if len(response.Body.Homes) > 0 {
		span.SetAttributes(attribute.Int("netatmo.homes_count", len(response.Body.Homes)))
	}

	span.SetStatus(codes.Ok, "homes data retrieved successfully")
	return &response, nil
}

// GetHomeStatus retrieves the current status of a specific home
func (c *Client) GetHomeStatus(ctx context.Context, homeID string) (*HomeStatusResponse, error) {
	tracer := otel.Tracer("netatmo")
	ctx, span := tracer.Start(ctx, "netatmo.GetHomeStatus",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("netatmo.api", "homestatus"),
			attribute.String("netatmo.home_id", homeID),
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

	// Add metadata about the response
	if len(response.Body.Home.Rooms) > 0 {
		span.SetAttributes(attribute.Int("netatmo.rooms_count", len(response.Body.Home.Rooms)))
	}

	span.SetStatus(codes.Ok, "home status retrieved successfully")
	return &response, nil
}
