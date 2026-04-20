package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tokenServer struct {
	*httptest.Server
	calls *int32
}

func newTokenServer(t *testing.T, token string, expiresAt time.Time) *tokenServer {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/app/installations/")
		assert.Contains(t, r.URL.Path, "/access_tokens")
		auth := r.Header.Get("Authorization")
		assert.True(t, len(auth) > 7 && auth[:7] == "Bearer ", "want Bearer <JWT>, got %q", auth)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      token,
			"expires_at": expiresAt.UTC().Format(time.RFC3339),
		})
	}))
	return &tokenServer{Server: srv, calls: &calls}
}

func TestInstallationCache_FetchesAndCaches(t *testing.T) {
	expires := time.Now().Add(55 * time.Minute)
	srv := newTokenServer(t, "ghs_abc123", expires)
	defer srv.Close()

	privPEM, _ := generateTestPEM(t)
	cache := NewInstallationCache(InstallationCacheConfig{
		AppID:      "42",
		PrivateKey: privPEM,
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Clock:      func() time.Time { return time.Now() },
		TokenTTL:   50 * time.Minute,
	})

	tok, err := cache.Token(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, "ghs_abc123", tok)

	// Second call within TTL: no additional HTTP request.
	tok2, err := cache.Token(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, "ghs_abc123", tok2)
	assert.Equal(t, int32(1), atomic.LoadInt32(srv.calls))
}

func TestInstallationCache_RefetchesOnExpiry(t *testing.T) {
	expires := time.Now().Add(1 * time.Hour)
	srv := newTokenServer(t, "ghs_first", expires)
	defer srv.Close()

	privPEM, _ := generateTestPEM(t)

	// Controllable clock.
	var virtualNow atomic.Pointer[time.Time]
	t0 := time.Now()
	virtualNow.Store(&t0)
	clock := func() time.Time { return *virtualNow.Load() }

	cache := NewInstallationCache(InstallationCacheConfig{
		AppID:      "42",
		PrivateKey: privPEM,
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Clock:      clock,
		TokenTTL:   50 * time.Minute,
	})

	_, err := cache.Token(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(srv.calls))

	// Advance clock past TTL; next call should refetch.
	future := t0.Add(time.Hour)
	virtualNow.Store(&future)
	_, err = cache.Token(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(srv.calls))
}

func TestInstallationCache_PropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"bad creds"}`)
	}))
	defer srv.Close()

	privPEM, _ := generateTestPEM(t)
	cache := NewInstallationCache(InstallationCacheConfig{
		AppID:      "42",
		PrivateKey: privPEM,
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Clock:      time.Now,
		TokenTTL:   50 * time.Minute,
	})

	_, err := cache.Token(context.Background(), 99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}
