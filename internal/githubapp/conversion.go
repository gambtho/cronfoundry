package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Conversion is the response from POST /app-manifests/{code}/conversions.
// Fields we don't use (e.g. node_id, html_url) are intentionally omitted.
type Conversion struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// Converter exchanges a one-time manifest code for an App's full credentials.
// The zero value is not usable; construct via NewConverter.
type Converter struct {
	BaseURL    string        // e.g. https://api.github.com
	HTTP       *http.Client  // injected for testing
	RetryDelay time.Duration // delay between attempts; one retry on 5xx
}

// NewConverter returns a Converter pointed at baseURL using the given client.
// baseURL has no trailing slash.
func NewConverter(baseURL string, hc *http.Client) *Converter {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Converter{BaseURL: baseURL, HTTP: hc, RetryDelay: 2 * time.Second}
}

// Convert exchanges the temporary code GitHub redirected with for the App's
// permanent credentials. One retry on 5xx; 4xx is surfaced immediately.
func (c *Converter) Convert(ctx context.Context, code string) (*Conversion, error) {
	url := fmt.Sprintf("%s/app-manifests/%s/conversions", c.BaseURL, code)

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.RetryDelay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		if err != nil {
			return nil, fmt.Errorf("githubapp: build request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("githubapp: do request: %w", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("githubapp: http %d: %s", resp.StatusCode, string(body))
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("githubapp: http %d: %s", resp.StatusCode, string(body))
		}
		var out Conversion
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("githubapp: decode: %w", err)
		}
		return &out, nil
	}
	return nil, lastErr
}
