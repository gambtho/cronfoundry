package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// InstallationCacheConfig configures a cache. Only AppID and PrivateKey are
// required; the rest have sensible defaults for production use.
type InstallationCacheConfig struct {
	AppID      string
	PrivateKey []byte           // PEM bytes
	BaseURL    string           // default: "https://api.github.com"
	HTTPClient *http.Client     // default: http.DefaultClient
	Clock      func() time.Time // default: time.Now
	TokenTTL   time.Duration    // default: 50 * time.Minute
}

// InstallationCache holds per-installation access tokens and mints new ones
// via the GitHub App JWT when cached tokens expire.
//
// Thread-safe; one cache per process.
type InstallationCache struct {
	cfg     InstallationCacheConfig
	mu      sync.Mutex
	entries map[int64]cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// NewInstallationCache constructs a cache with defaults applied for any
// zero-valued Config field.
func NewInstallationCache(cfg InstallationCacheConfig) *InstallationCache {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.github.com"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = 50 * time.Minute
	}
	return &InstallationCache{cfg: cfg, entries: map[int64]cachedToken{}}
}

// Token returns an installation access token for installID, minting a fresh
// one via the GitHub App JWT exchange when the cache entry is missing or
// expired.
func (c *InstallationCache) Token(ctx context.Context, installID int64) (string, error) {
	c.mu.Lock()
	entry, ok := c.entries[installID]
	now := c.cfg.Clock()
	c.mu.Unlock()

	if ok && now.Before(entry.expiresAt) {
		return entry.token, nil
	}

	tok, expires, err := c.fetch(ctx, installID)
	if err != nil {
		return "", err
	}

	// Use the shorter of (server's expires_at, TokenTTL from now).
	ttlExpiry := now.Add(c.cfg.TokenTTL)
	if expires.Before(ttlExpiry) {
		ttlExpiry = expires
	}

	c.mu.Lock()
	c.entries[installID] = cachedToken{token: tok, expiresAt: ttlExpiry}
	c.mu.Unlock()
	return tok, nil
}

// fetch calls POST /app/installations/{id}/access_tokens and parses the
// response.
func (c *InstallationCache) fetch(ctx context.Context, installID int64) (string, time.Time, error) {
	jwt, err := AppJWT(c.cfg.AppID, c.cfg.PrivateKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: mint JWT: %w", err)
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.cfg.BaseURL, installID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", time.Time{}, fmt.Errorf("github: installation: http %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: decode: %w", err)
	}
	expires, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: parse expires_at: %w", err)
	}
	return payload.Token, expires, nil
}
