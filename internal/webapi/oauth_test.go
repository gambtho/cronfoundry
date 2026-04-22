package webapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/testdb"
	"github.com/gambtho/cronfoundry/internal/webapi"
)

// newTestDeps builds a Deps with a stub GitHub server replacing the real API.
// This helper is for tests that DO NOT require a DB (login redirect, CSRF
// mismatch, logout). Tests that exercise the full OAuth callback must use
// newTestDepsWithDB so resolveRole + UpsertUserOnLogin have a real DB.
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

// newTestDepsWithDB builds a Deps wired to a real Postgres pool. Callers are
// responsible for seeding an organization + any app_user rows before the
// request under test is issued.
func newTestDepsWithDB(t *testing.T, pool *pgxpool.Pool, githubBase string) webapi.Deps {
	t.Helper()
	return webapi.Deps{
		MasterKey:         testKey(),
		OAuthClientID:     "test-client-id",
		OAuthClientSecret: "test-client-secret",
		AdminLogins:       []string{},
		ViewerLogins:      []string{},
		GitHubAPIBase:     githubBase,
		Queries:           dbgen.New(pool),
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
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	ctx := context.Background()
	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	require.NoError(t, err)
	_, err = q.CreateUser(ctx, dbgen.CreateUserParams{
		OrgID:       org.ID,
		GithubLogin: "octocat",
		Role:        "admin",
	})
	require.NoError(t, err)

	stub := stubGitHub(t, "octocat")
	defer stub.Close()

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, newTestDepsWithDB(t, pool, stub.URL))

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
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	// Deliberately do NOT seed a user row — GetUserRole returns ErrNoRows,
	// resolveRole collapses to "", callback returns 403.

	stub := stubGitHub(t, "notallowed")
	defer stub.Close()

	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, newTestDepsWithDB(t, pool, stub.URL))

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

func TestOAuth_Callback_SessionCookieIs7Days(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()
	seedOrg(t, pool)

	ctx := context.Background()
	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	require.NoError(t, err)
	_, err = q.CreateUser(ctx, dbgen.CreateUserParams{
		OrgID:       org.ID,
		GithubLogin: "octocat",
		Role:        "admin",
	})
	require.NoError(t, err)

	stub := stubGitHub(t, "octocat")
	defer stub.Close()

	mux := http.NewServeMux()
	deps := newTestDepsWithDB(t, pool, stub.URL)
	webapi.RegisterRoutes(mux, deps)

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

	require.Equal(t, http.StatusFound, cbRR.Code, "body: %s", cbRR.Body.String())

	var sessionCookie *http.Cookie
	for _, c := range cbRR.Result().Cookies() {
		if c.Name == "cf_session" {
			sessionCookie = c
		}
	}
	require.NotNil(t, sessionCookie, "cf_session cookie not set")

	// Assert MaxAge is 7 days (7 * 24 * 3600 = 604800 seconds)
	expectedMaxAge := 7 * 24 * 3600
	assert.Equal(t, expectedMaxAge, sessionCookie.MaxAge, "session cookie MaxAge should be 7 days")

	// Also verify the signed session's Exp claim lines up — the cookie MaxAge
	// and the JWT expiry must agree, otherwise one will outlive the other.
	claims, err := webapi.VerifySession(sessionCookie.Value, deps.MasterKey)
	require.NoError(t, err, "session cookie value must verify")
	ttl := time.Until(time.Unix(claims.Exp, 0))
	// Small clock-skew slack — signing happens ~ms before this check.
	assert.InDelta(t, (7 * 24 * time.Hour).Seconds(), ttl.Seconds(), 5.0,
		"signed session Exp must match cookie MaxAge within a few seconds")
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
