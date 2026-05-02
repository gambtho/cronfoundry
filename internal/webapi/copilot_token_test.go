package webapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

// fakeCopilotMint stubs api.github.com/copilot_internal/v2/token. Captures
// the Authorization header so tests can assert we sent the OAuth token.
type fakeCopilotMint struct {
	mintCalled    int
	lastAuthValue string
	respToken     string
	respExpiresAt int64
	respStatus    int
}

func (f *fakeCopilotMint) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mintCalled++
		f.lastAuthValue = r.Header.Get("Authorization")
		if f.respStatus == 0 || f.respStatus == http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      f.respToken,
				"expires_at": f.respExpiresAt,
			})
			return
		}
		w.WriteHeader(f.respStatus)
	})
}

// Cached IDE token still inside its expiry window: resolver returns it
// without hitting GitHub.
func TestResolveCopilotToken_UsesCachedIDEToken(t *testing.T) {
	mint := &fakeCopilotMint{}
	gh := httptest.NewServer(mint.handler())
	defer gh.Close()

	store := newTestMemStore()
	future := strconv.FormatInt(time.Now().Add(20*time.Minute).Unix(), 10)
	_ = store.Put(context.Background(), "copilot-access-token", "gho_oauth_long_lived")
	_ = store.Put(context.Background(), "copilot-ide-token", "tok_cached_fresh")
	_ = store.Put(context.Background(), "copilot-ide-expiry", future)

	got, exp, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", &gh.URL)
	require.NoError(t, err)
	assert.Equal(t, "tok_cached_fresh", got)
	assert.False(t, exp.IsZero())
	assert.Equal(t, 0, mint.mintCalled, "should not have hit GitHub")
}

// Fresh install: no IDE token cached, but OAuth token present. Resolver
// mints via copilot_internal/v2/token, persists to cache, returns.
func TestResolveCopilotToken_MintsOnFirstCall(t *testing.T) {
	mint := &fakeCopilotMint{
		respToken:     "tok_freshly_minted",
		respExpiresAt: time.Now().Add(25 * time.Minute).Unix(),
	}
	gh := httptest.NewServer(mint.handler())
	defer gh.Close()

	store := newTestMemStore()
	_ = store.Put(context.Background(), "copilot-access-token", "gho_oauth_long_lived")

	got, exp, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", &gh.URL)
	require.NoError(t, err)
	assert.Equal(t, "tok_freshly_minted", got)
	assert.False(t, exp.IsZero())
	assert.Equal(t, 1, mint.mintCalled)
	assert.Equal(t, "token gho_oauth_long_lived", mint.lastAuthValue,
		"OAuth token must be sent in 'token <value>' form, not 'Bearer'")

	// Cache should now hold the IDE token + expiry.
	cached, _ := store.Get(context.Background(), "copilot-ide-token")
	assert.Equal(t, "tok_freshly_minted", cached)
	cachedExp, _ := store.Get(context.Background(), "copilot-ide-expiry")
	assert.NotEmpty(t, cachedExp)
}

// Cached IDE token expired: resolver re-mints.
func TestResolveCopilotToken_RemintWhenExpired(t *testing.T) {
	mint := &fakeCopilotMint{
		respToken:     "tok_renewed",
		respExpiresAt: time.Now().Add(25 * time.Minute).Unix(),
	}
	gh := httptest.NewServer(mint.handler())
	defer gh.Close()

	store := newTestMemStore()
	past := strconv.FormatInt(time.Now().Add(-1*time.Minute).Unix(), 10)
	_ = store.Put(context.Background(), "copilot-access-token", "gho_oauth_long_lived")
	_ = store.Put(context.Background(), "copilot-ide-token", "tok_old_expired")
	_ = store.Put(context.Background(), "copilot-ide-expiry", past)

	got, _, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", &gh.URL)
	require.NoError(t, err)
	assert.Equal(t, "tok_renewed", got)
	assert.Equal(t, 1, mint.mintCalled)

	cached, _ := store.Get(context.Background(), "copilot-ide-token")
	assert.Equal(t, "tok_renewed", cached)
}

// OAuth token missing: clear error pointing the operator at the right
// remediation. (Don't mask this as a 503 — re-running connect-copilot is
// the only fix.)
func TestResolveCopilotToken_NoOAuthToken(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("mint endpoint should not be hit when oauth token is missing")
	}))
	defer gh.Close()

	store := newTestMemStore()
	_ = store.Put(context.Background(), "copilot-access-token", "")

	_, _, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", &gh.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connect-copilot")
}

// Mint endpoint returns 401: OAuth token has been revoked GitHub-side.
// Resolver surfaces a remediation-friendly error.
func TestResolveCopilotToken_MintReturns401(t *testing.T) {
	mint := &fakeCopilotMint{respStatus: http.StatusUnauthorized}
	gh := httptest.NewServer(mint.handler())
	defer gh.Close()

	store := newTestMemStore()
	_ = store.Put(context.Background(), "copilot-access-token", "gho_revoked")

	_, _, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", &gh.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "connect-copilot")
}

// Backwards compat with installs that still have the legacy
// copilot-refresh-token + copilot-expiry slots from the pre-IDE-token
// implementation. The new resolver ignores those keys; the OAuth access
// token in <prefix>-access-token is still used to mint a fresh IDE token.
func TestResolveCopilotToken_IgnoresLegacyRefreshSlots(t *testing.T) {
	mint := &fakeCopilotMint{
		respToken:     "tok_for_legacy_install",
		respExpiresAt: time.Now().Add(25 * time.Minute).Unix(),
	}
	gh := httptest.NewServer(mint.handler())
	defer gh.Close()

	store := newTestMemStore()
	_ = store.Put(context.Background(), "copilot-access-token", "gho_oauth_long_lived")
	_ = store.Put(context.Background(), "copilot-refresh-token", "") // legacy
	past := strconv.FormatInt(time.Now().Add(-1*time.Hour).Unix(), 10)
	_ = store.Put(context.Background(), "copilot-expiry", past) // legacy

	got, _, err := webapi.ResolveCopilotToken(context.Background(), store, "copilot", &gh.URL)
	require.NoError(t, err)
	assert.Equal(t, "tok_for_legacy_install", got)
}
