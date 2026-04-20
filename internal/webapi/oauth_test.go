package webapi_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

// newTestDeps builds a Deps with a stub GitHub server replacing the real API.
func newTestDeps(t *testing.T, githubBase string) webapi.Deps {
	t.Helper()
	return webapi.Deps{
		MasterKey:         testKey(),
		OAuthClientID:     "test-client-id",
		OAuthClientSecret: "test-client-secret",
		AdminLogins:       []string{"octocat"},
		ViewerLogins:      []string{"viewer1"},
		GitHubAPIBase:     githubBase,
	}
}

// stubGitHub returns a test server that mocks GitHub OAuth token + user endpoints.
func stubGitHub(t *testing.T, login string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"ghu_test","token_type":"bearer"}`))
		case "/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login":"` + login + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestOAuth_Login_RedirectsToGitHub(t *testing.T) {
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, newTestDeps(t, ""))

	req := httptest.NewRequest("GET", "/oauth/login", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusFound, rr.Code)
	loc := rr.Header().Get("Location")
	assert.Contains(t, loc, "github.com/login/oauth/authorize")
	assert.Contains(t, loc, "client_id=test-client-id")

	// State cookie must be set.
	var stateCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "oauth_state" {
			stateCookie = c
		}
	}
	require.NotNil(t, stateCookie, "oauth_state cookie not set")
}

func TestOAuth_Callback_SetsSessionCookie(t *testing.T) {
	stub := stubGitHub(t, "octocat")
	defer stub.Close()

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, newTestDeps(t, stub.URL))

	// Get state from /oauth/login.
	loginReq := httptest.NewRequest("GET", "/oauth/login", nil)
	loginRR := httptest.NewRecorder()
	mux.ServeHTTP(loginRR, loginReq)
	require.Equal(t, http.StatusFound, loginRR.Code)

	var stateCookie *http.Cookie
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == "oauth_state" {
			stateCookie = c
		}
	}
	require.NotNil(t, stateCookie)

	cbURL := "/oauth/callback?code=testcode&state=" + url.QueryEscape(stateCookie.Value)
	cbReq := httptest.NewRequest("GET", cbURL, nil)
	cbReq.AddCookie(stateCookie)
	cbRR := httptest.NewRecorder()
	mux.ServeHTTP(cbRR, cbReq)

	assert.Equal(t, http.StatusFound, cbRR.Code, "body: %s", cbRR.Body.String())

	var sessionCookie *http.Cookie
	for _, c := range cbRR.Result().Cookies() {
		if c.Name == "cf_session" {
			sessionCookie = c
		}
	}
	require.NotNil(t, sessionCookie, "cf_session cookie not set")

	claims, err := webapi.VerifySession(sessionCookie.Value, testKey())
	require.NoError(t, err)
	assert.Equal(t, "octocat", claims.Login)
	assert.Equal(t, "admin", claims.Role)
}

func TestOAuth_Callback_RejectsCSRFMismatch(t *testing.T) {
	stub := stubGitHub(t, "octocat")
	defer stub.Close()

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, newTestDeps(t, stub.URL))

	// Use a valid state cookie but pass a different state in the query.
	legitState, err := webapi.SignOAuthState(testKey(), time.Minute)
	require.NoError(t, err)
	cbReq := httptest.NewRequest("GET", "/oauth/callback?code=x&state=wrong", nil)
	cbReq.AddCookie(&http.Cookie{Name: "oauth_state", Value: legitState})
	cbRR := httptest.NewRecorder()
	mux.ServeHTTP(cbRR, cbReq)

	assert.Equal(t, http.StatusBadRequest, cbRR.Code)
}

func TestOAuth_Callback_RejectsUnallowedLogin(t *testing.T) {
	stub := stubGitHub(t, "notallowed")
	defer stub.Close()

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, newTestDeps(t, stub.URL))

	loginReq := httptest.NewRequest("GET", "/oauth/login", nil)
	loginRR := httptest.NewRecorder()
	mux.ServeHTTP(loginRR, loginReq)

	var stateCookie *http.Cookie
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == "oauth_state" {
			stateCookie = c
		}
	}
	require.NotNil(t, stateCookie)

	cbURL := "/oauth/callback?code=testcode&state=" + url.QueryEscape(stateCookie.Value)
	cbReq := httptest.NewRequest("GET", cbURL, nil)
	cbReq.AddCookie(stateCookie)
	cbRR := httptest.NewRecorder()
	mux.ServeHTTP(cbRR, cbReq)

	assert.Equal(t, http.StatusForbidden, cbRR.Code)
}

func TestOAuth_Logout_ClearsSession(t *testing.T) {
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, newTestDeps(t, ""))

	req := httptest.NewRequest("GET", "/oauth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: "whatever"})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusFound, rr.Code)
	for _, c := range rr.Result().Cookies() {
		if c.Name == "cf_session" {
			assert.True(t, c.MaxAge < 0 || c.Value == "", "cf_session cookie not cleared")
		}
	}
}
