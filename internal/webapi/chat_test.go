package webapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

func TestChatInfo_DisabledByDefault(t *testing.T) {
	key := testKey()
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:         key,
		OAuthClientID:     "cid",
		OAuthClientSecret: "csec",
		AdminLogins:       []string{"alice"},
	})

	req := requestWithSession(t, "alice", "admin")
	req.URL.Path = "/api/chat/info"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, false, got["enabled"], "chat should be disabled by default")
}

func TestChatInfo_EnabledExposesModel(t *testing.T) {
	key := testKey()
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:         key,
		OAuthClientID:     "cid",
		OAuthClientSecret: "csec",
		AdminLogins:       []string{"alice"},
		Chat: webapi.ChatConfig{
			Enabled:      true,
			Provider:     "anthropic",
			Model:        "claude-sonnet-4-5",
			APIKeySecret: "anthropic_key",
		},
	})

	req := requestWithSession(t, "alice", "admin")
	req.URL.Path = "/api/chat/info"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, true, got["enabled"])
	assert.Equal(t, "claude-sonnet-4-5", got["model"])
}

func TestChatInfo_RequiresSession(t *testing.T) {
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:         testKey(),
		OAuthClientID:     "cid",
		OAuthClientSecret: "csec",
		AdminLogins:       []string{"alice"},
	})

	req := httptest.NewRequest("GET", "/api/chat/info", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestChatStream_DisabledReturns503(t *testing.T) {
	key := testKey()
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:         key,
		OAuthClientID:     "cid",
		OAuthClientSecret: "csec",
		AdminLogins:       []string{"alice"},
		// No Chat config — feature is off.
	})

	body := strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", "/api/chat/stream", body)
	cookie, err := webapi.SignSession(webapi.SessionClaims{Login: "alice", Role: "admin"}, key, 0)
	require.NoError(t, err)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	// CSRF: the route requires the X-CSRF-Token header to match the cookie
	// when PUBLIC_BASE_URL gates Origin checks. In tests with no public
	// base URL the Origin check is skipped, but the cookie+header pair
	// is still validated.
	req.AddCookie(&http.Cookie{Name: "cf_csrf", Value: "test-csrf-token"})
	req.Header.Set("X-CSRF-Token", "test-csrf-token")
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
}
