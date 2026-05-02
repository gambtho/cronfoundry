package webapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestResolveCopilotToken_FreshToken(t *testing.T) {
	store := newTestMemStore()
	future := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	_ = store.Put(context.Background(), "copilot-access-token", "ghu_fresh")
	_ = store.Put(context.Background(), "copilot-expiry", future)

	accessToken, expiresAt, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", nil)
	require.NoError(t, err)
	assert.Equal(t, "ghu_fresh", accessToken)
	assert.False(t, expiresAt.IsZero())
}

func TestResolveCopilotToken_ExpiredRefreshes(t *testing.T) {
	var refreshCalled bool
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ghu_new","refresh_token":"ghu_newrefresh","expires_in":28800}`))
	}))
	defer gh.Close()

	store := newTestMemStore()
	past := strconv.FormatInt(time.Now().Add(-1*time.Minute).Unix(), 10)
	_ = store.Put(context.Background(), "copilot-access-token", "ghu_stale")
	_ = store.Put(context.Background(), "copilot-refresh-token", "ghu_oldrefresh")
	_ = store.Put(context.Background(), "copilot-expiry", past)

	accessToken, _, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", &gh.URL)
	require.NoError(t, err)
	assert.True(t, refreshCalled)
	assert.Equal(t, "ghu_new", accessToken)

	v, _ := store.Get(context.Background(), "copilot-access-token")
	assert.Equal(t, "ghu_new", v)
}

func TestResolveCopilotToken_RefreshFailure(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer gh.Close()

	store := newTestMemStore()
	past := strconv.FormatInt(time.Now().Add(-1*time.Minute).Unix(), 10)
	_ = store.Put(context.Background(), "copilot-access-token", "ghu_stale")
	_ = store.Put(context.Background(), "copilot-refresh-token", "ghu_oldrefresh")
	_ = store.Put(context.Background(), "copilot-expiry", past)

	_, _, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", &gh.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh")
}

// GitHub's public Copilot OAuth client (Iv1.b507a08c87ecfe98) issues
// access tokens with an expires_in but no refresh_token via device flow.
// When the stored expiry has passed, the resolver must NOT 503 — it
// returns the cached access token and lets the downstream Copilot API
// surface the actual "expired/revoked" signal via 401. Forcing a 503
// here is what crashed the round-3 dogfood smoke run.
func TestResolveCopilotToken_ExpiredButNoRefreshToken(t *testing.T) {
	store := newTestMemStore()
	past := strconv.FormatInt(time.Now().Add(-1*time.Hour).Unix(), 10)
	_ = store.Put(context.Background(), "copilot-access-token", "ghu_cached")
	_ = store.Put(context.Background(), "copilot-refresh-token", "") // empty — Copilot device flow
	_ = store.Put(context.Background(), "copilot-expiry", past)

	accessToken, _, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", nil)
	require.NoError(t, err)
	assert.Equal(t, "ghu_cached", accessToken,
		"resolver must return cached token when expiry passed and no refresh token available")
}
