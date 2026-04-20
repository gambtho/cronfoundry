# P3a Auth Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add GitHub OAuth login, signed session cookies, `requireSession` middleware, and `GET /api/me` to CronFoundry's HTTP server.

**Architecture:** A new `internal/webapi/` package registers `/oauth/*` and `/api/*` routes on the same `http.ServeMux` as the existing `internal/api/` runner-facing routes. Sessions are stateless HMAC-SHA256-signed cookies using the existing master key. Role assignment is env-var-driven (no DB changes).

**Tech Stack:** Go stdlib `net/http`, `crypto/hmac` + `crypto/sha256`, `encoding/json`, `encoding/base64`; existing `github.com/jackc/pgx/v5`, `github.com/stretchr/testify`; no new Go dependencies.

---

## File Map

| File | New/Modify | Responsibility |
|------|-----------|---------------|
| `internal/webapi/session.go` | Create | `SessionClaims` struct; sign/verify cookie helpers |
| `internal/webapi/auth.go` | Create | `requireSession` middleware; `SessionClaimsFromContext` |
| `internal/webapi/oauth.go` | Create | `/oauth/login`, `/oauth/callback`, `/oauth/logout` handlers |
| `internal/webapi/me.go` | Create | `GET /api/me` handler |
| `internal/webapi/server.go` | Create | `Deps` struct; `RegisterRoutes(mux, deps)` |
| `internal/webapi/session_test.go` | Create | Unit tests for sign/verify |
| `internal/webapi/auth_test.go` | Create | Unit tests for `requireSession` middleware |
| `internal/webapi/oauth_test.go` | Create | Unit tests for OAuth handlers (stubbed GitHub) |
| `internal/webapi/me_test.go` | Create | Unit tests for `/api/me` |
| `cmd/cronfoundry/serve.go` | Modify | Load new env vars; pass `webapi.Deps`; call `webapi.RegisterRoutes` |
| `cmd/cronfoundry/serve_test.go` | Modify | Add `TestServe_APIMe` integration test; add missing-env tests |

---

## Task 1: `session.go` — cookie sign/verify

**Files:**
- Create: `internal/webapi/session.go`
- Create: `internal/webapi/session_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/webapi/session_test.go`:

```go
package webapi_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

func testKey() []byte { return []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") }

func TestSession_RoundTrip(t *testing.T) {
	claims := webapi.SessionClaims{Login: "alice", Role: "admin"}
	cookie, err := webapi.SignSession(claims, testKey(), 24*time.Hour)
	require.NoError(t, err)
	got, err := webapi.VerifySession(cookie, testKey())
	require.NoError(t, err)
	assert.Equal(t, "alice", got.Login)
	assert.Equal(t, "admin", got.Role)
}

func TestSession_Expired(t *testing.T) {
	claims := webapi.SessionClaims{Login: "alice", Role: "admin"}
	cookie, err := webapi.SignSession(claims, testKey(), -1*time.Second)
	require.NoError(t, err)
	_, err = webapi.VerifySession(cookie, testKey())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestSession_TamperedSignature(t *testing.T) {
	claims := webapi.SessionClaims{Login: "alice", Role: "admin"}
	cookie, err := webapi.SignSession(claims, testKey(), 24*time.Hour)
	require.NoError(t, err)
	// Flip the last byte of the signature.
	b := []byte(cookie)
	b[len(b)-1] ^= 0xFF
	_, err = webapi.VerifySession(string(b), testKey())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid signature")
}

func TestSession_TamperedPayload(t *testing.T) {
	claims := webapi.SessionClaims{Login: "alice", Role: "admin"}
	cookie, err := webapi.SignSession(claims, testKey(), 24*time.Hour)
	require.NoError(t, err)
	// Replace login in cookie value by corrupting the first byte of payload.
	b := []byte(cookie)
	b[0] ^= 0xFF
	_, err = webapi.VerifySession(string(b), testKey())
	require.Error(t, err)
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd /path/to/worktree
go test ./internal/webapi/... 2>&1
```

Expected: `cannot find package` or compile error — package doesn't exist yet.

- [ ] **Step 3: Implement `session.go`**

Create `internal/webapi/session.go`:

```go
// Package webapi hosts the /api/* and /oauth/* browser-facing HTTP surface.
// Runner-facing endpoints live in internal/api.
package webapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SessionClaims is the payload embedded in the cf_session cookie.
type SessionClaims struct {
	Login string `json:"login"`
	Role  string `json:"role"`
	Exp   int64  `json:"exp"` // Unix seconds
}

// SignSession encodes claims as a signed cookie value:
//
//	base64url(JSON) + "." + base64url(HMAC-SHA256(payload, key))
func SignSession(claims SessionClaims, key []byte, ttl time.Duration) (string, error) {
	claims.Exp = time.Now().Add(ttl).Unix()
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal session: %w", err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	sig := sign([]byte(enc), key)
	return enc + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifySession parses and validates a signed cookie value produced by SignSession.
func VerifySession(cookie string, key []byte) (SessionClaims, error) {
	dot := lastDot(cookie)
	if dot < 0 {
		return SessionClaims{}, errors.New("invalid signature: malformed cookie")
	}
	enc, sigEnc := cookie[:dot], cookie[dot+1:]

	sigGot, err := base64.RawURLEncoding.DecodeString(sigEnc)
	if err != nil {
		return SessionClaims{}, errors.New("invalid signature: bad encoding")
	}
	sigExpected := sign([]byte(enc), key)
	if !hmac.Equal(sigGot, sigExpected) {
		return SessionClaims{}, errors.New("invalid signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return SessionClaims{}, fmt.Errorf("invalid session: %w", err)
	}
	var claims SessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return SessionClaims{}, fmt.Errorf("invalid session: %w", err)
	}
	if time.Now().Unix() > claims.Exp {
		return SessionClaims{}, errors.New("session expired")
	}
	return claims, nil
}

func sign(data, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/webapi/... -run TestSession -v
```

Expected: all 4 `TestSession_*` tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/session.go internal/webapi/session_test.go
git commit -m "feat(webapi): session sign/verify helpers"
```

---

## Task 2: `auth.go` — `requireSession` middleware

**Files:**
- Create: `internal/webapi/auth.go`
- Create: `internal/webapi/auth_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/webapi/auth_test.go`:

```go
package webapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

func okHandler(w http.ResponseWriter, r *http.Request) {
	claims := webapi.SessionClaimsFromContext(r.Context())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(claims.Login))
}

func TestRequireSession_ValidCookie(t *testing.T) {
	key := testKey()
	claims := webapi.SessionClaims{Login: "bob", Role: "viewer"}
	cookie, err := webapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	rr := httptest.NewRecorder()

	webapi.RequireSession(key, http.HandlerFunc(okHandler)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "bob", rr.Body.String())
}

func TestRequireSession_MissingCookie(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()

	webapi.RequireSession(testKey(), http.HandlerFunc(okHandler)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireSession_ExpiredCookie(t *testing.T) {
	key := testKey()
	claims := webapi.SessionClaims{Login: "bob", Role: "viewer"}
	cookie, err := webapi.SignSession(claims, key, -time.Second)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	rr := httptest.NewRecorder()

	webapi.RequireSession(key, http.HandlerFunc(okHandler)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireSession_TamperedCookie(t *testing.T) {
	key := testKey()
	claims := webapi.SessionClaims{Login: "bob", Role: "viewer"}
	cookie, err := webapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)
	b := []byte(cookie)
	b[len(b)-1] ^= 0xFF

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: string(b)})
	rr := httptest.NewRecorder()

	webapi.RequireSession(key, http.HandlerFunc(okHandler)).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/webapi/... -run TestRequireSession -v
```

Expected: compile error — `RequireSession` and `SessionClaimsFromContext` not defined.

- [ ] **Step 3: Implement `auth.go`**

Create `internal/webapi/auth.go`:

```go
package webapi

import (
	"context"
	"net/http"
)

type ctxKey int

const sessionKey ctxKey = 0

// RequireSession is middleware that validates the cf_session cookie.
// Attaches SessionClaims to the request context on success; returns 401 otherwise.
func RequireSession(masterKey []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("cf_session")
		if err != nil {
			http.Error(w, "missing session", http.StatusUnauthorized)
			return
		}
		claims, err := VerifySession(c.Value, masterKey)
		if err != nil {
			http.Error(w, "invalid session: "+err.Error(), http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SessionClaimsFromContext returns the claims attached by RequireSession.
// Returns zero value if the middleware was not applied.
func SessionClaimsFromContext(ctx context.Context) SessionClaims {
	c, _ := ctx.Value(sessionKey).(SessionClaims)
	return c
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/webapi/... -run TestRequireSession -v
```

Expected: all 4 `TestRequireSession_*` tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/auth.go internal/webapi/auth_test.go
git commit -m "feat(webapi): requireSession middleware"
```

---

## Task 3: `me.go` — `GET /api/me`

**Files:**
- Create: `internal/webapi/me.go`
- Create: `internal/webapi/me_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/webapi/me_test.go`:

```go
package webapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/webapi"
)

func requestWithSession(t *testing.T, login, role string) *http.Request {
	t.Helper()
	key := testKey()
	claims := webapi.SessionClaims{Login: login, Role: role}
	cookie, err := webapi.SignSession(claims, key, time.Hour)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	return req
}

func TestMe_Admin(t *testing.T) {
	key := testKey()
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:         key,
		OAuthClientID:     "cid",
		OAuthClientSecret: "csec",
		AdminLogins:       []string{"alice"},
	})

	req := requestWithSession(t, "alice", "admin")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "alice", got["login"])
	assert.Equal(t, "admin", got["role"])
}

func TestMe_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	webapi.RegisterRoutes(mux, webapi.Deps{
		MasterKey:         testKey(),
		OAuthClientID:     "cid",
		OAuthClientSecret: "csec",
		AdminLogins:       []string{"alice"},
	})

	req := httptest.NewRequest("GET", "/api/me", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/webapi/... -run TestMe -v
```

Expected: compile error — `RegisterRoutes` and `Deps` not defined.

- [ ] **Step 3: Implement `me.go` and stub `server.go`**

Create `internal/webapi/me.go`:

```go
package webapi

import (
	"encoding/json"
	"net/http"
)

type meHandler struct{ masterKey []byte }

func (h meHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims := SessionClaimsFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"login": claims.Login,
		"role":  claims.Role,
	})
}
```

Create `internal/webapi/server.go`:

```go
package webapi

import "net/http"

// Deps holds everything webapi handlers need.
type Deps struct {
	MasterKey         []byte
	OAuthClientID     string
	OAuthClientSecret string
	AdminLogins       []string
	ViewerLogins      []string
}

// RegisterRoutes registers /oauth/* and /api/* on mux.
func RegisterRoutes(mux *http.ServeMux, deps Deps) {
	session := func(h http.Handler) http.Handler {
		return RequireSession(deps.MasterKey, h)
	}

	mux.Handle("GET /api/me", session(meHandler{deps.MasterKey}))

	oh := oauthHandlers{deps: deps}
	mux.HandleFunc("GET /oauth/login", oh.login)
	mux.HandleFunc("GET /oauth/callback", oh.callback)
	mux.HandleFunc("GET /oauth/logout", oh.logout)
}
```

- [ ] **Step 4: Run tests to confirm they fail on missing `oauthHandlers`**

```bash
go test ./internal/webapi/... -run TestMe -v
```

Expected: compile error — `oauthHandlers` not defined yet (next task).

- [ ] **Note:** Proceed to Task 4 before running tests again.

---

## Task 4: `oauth.go` — OAuth handlers

**Files:**
- Create: `internal/webapi/oauth.go`
- Create: `internal/webapi/oauth_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/webapi/oauth_test.go`:

```go
package webapi_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

	// First get the state from /oauth/login.
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

	// Decode state value from cookie (it's a signed value; extract the payload).
	stateVal := stateCookie.Value
	dot := strings.LastIndex(stateVal, ".")
	require.Positive(t, dot)
	rawState := stateVal[:dot] // use the full signed cookie value as state param

	cbURL := "/oauth/callback?code=testcode&state=" + url.QueryEscape(stateVal)
	cbReq := httptest.NewRequest("GET", cbURL, nil)
	cbReq.AddCookie(stateCookie)
	cbRR := httptest.NewRecorder()
	mux.ServeHTTP(cbRR, cbReq)

	assert.Equal(t, http.StatusFound, cbRR.Code, "body: %s", cbRR.Body.String())
	_ = rawState // used above

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

	// Set state cookie to "legit" but pass different state in query.
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
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/webapi/... 2>&1 | head -30
```

Expected: compile errors — `oauthHandlers`, `GitHubAPIBase`, `SignOAuthState` not defined.

- [ ] **Step 3: Implement `oauth.go`**

Create `internal/webapi/oauth.go`:

```go
package webapi

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	githubAuthorizeURL    = "https://github.com/login/oauth/authorize"
	githubAccessTokenURL  = "https://github.com/login/oauth/access_token"
	githubUserURL         = "https://api.github.com/user"
)

type oauthHandlers struct{ deps Deps }

// login initiates the OAuth flow: generate state, set cookie, redirect to GitHub.
func (h oauthHandlers) login(w http.ResponseWriter, r *http.Request) {
	state, err := SignOAuthState(h.deps.MasterKey, 10*time.Minute)
	if err != nil {
		http.Error(w, "state generation failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   !isLocalhost(r.Host),
	})
	params := url.Values{
		"client_id": {h.deps.OAuthClientID},
		"state":     {state},
		"scope":     {"read:user"},
	}
	http.Redirect(w, r, githubAuthorizeURL+"?"+params.Encode(), http.StatusFound)
}

// callback handles the GitHub redirect: verify state, exchange code, set session.
func (h oauthHandlers) callback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil {
		http.Error(w, "missing state cookie", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("state") != stateCookie.Value {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", MaxAge: -1, Path: "/"})

	accessToken, err := h.exchangeCode(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	login, err := h.fetchLogin(r.Context(), accessToken)
	if err != nil {
		http.Error(w, "user fetch failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	role := h.deps.resolveRole(login)
	if role == "" {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	session, err := SignSession(SessionClaims{Login: login, Role: role}, h.deps.MasterKey, 24*time.Hour)
	if err != nil {
		http.Error(w, "session creation failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "cf_session",
		Value:    session,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   !isLocalhost(r.Host),
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// logout clears the session cookie and redirects to /oauth/login.
func (h oauthHandlers) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "cf_session", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/oauth/login", http.StatusFound)
}

// exchangeCode swaps an OAuth code for an access token.
func (h oauthHandlers) exchangeCode(ctx context.Context, code string) (string, error) {
	base := githubAccessTokenURL
	if h.deps.GitHubAPIBase != "" {
		base = h.deps.GitHubAPIBase + "/login/oauth/access_token"
	}
	params := url.Values{
		"client_id":     {h.deps.OAuthClientID},
		"client_secret": {h.deps.OAuthClientSecret},
		"code":          {code},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base, strings.NewReader(params.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("github error: %s", result.Error)
	}
	return result.AccessToken, nil
}

// fetchLogin calls /user with the access token and returns the GitHub login.
func (h oauthHandlers) fetchLogin(ctx context.Context, accessToken string) (string, error) {
	base := githubUserURL
	if h.deps.GitHubAPIBase != "" {
		base = h.deps.GitHubAPIBase + "/user"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return "", fmt.Errorf("decode user: %w", err)
	}
	return user.Login, nil
}

// SignOAuthState generates a signed random state token for CSRF protection.
func SignOAuthState(key []byte, ttl time.Duration) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(b)
	return SignSession(SessionClaims{Login: nonce, Role: "state"}, key, ttl)
}

func isLocalhost(host string) bool {
	h := strings.Split(host, ":")[0]
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
```

- [ ] **Step 4: Add `GitHubAPIBase` to `Deps` and `resolveRole` in `server.go`**

Update `internal/webapi/server.go` to add the missing field and helper:

```go
package webapi

import "net/http"

// Deps holds everything webapi handlers need.
type Deps struct {
	MasterKey         []byte
	OAuthClientID     string
	OAuthClientSecret string
	AdminLogins       []string
	ViewerLogins      []string
	// GitHubAPIBase overrides the GitHub API base URL in tests. Empty = real GitHub.
	GitHubAPIBase string
}

// resolveRole returns "admin", "viewer", or "" (not allowed).
func (d Deps) resolveRole(login string) string {
	for _, l := range d.AdminLogins {
		if l == login {
			return "admin"
		}
	}
	for _, l := range d.ViewerLogins {
		if l == login {
			return "viewer"
		}
	}
	return ""
}

// RegisterRoutes registers /oauth/* and /api/* on mux.
func RegisterRoutes(mux *http.ServeMux, deps Deps) {
	session := func(h http.Handler) http.Handler {
		return RequireSession(deps.MasterKey, h)
	}

	mux.Handle("GET /api/me", session(meHandler{deps.MasterKey}))

	oh := oauthHandlers{deps: deps}
	mux.HandleFunc("GET /oauth/login", oh.login)
	mux.HandleFunc("GET /oauth/callback", oh.callback)
	mux.HandleFunc("GET /oauth/logout", oh.logout)
}
```

- [ ] **Step 5: Fix missing `context` import in `oauth.go`**

The `exchangeCode` and `fetchLogin` methods use `context.Context` — add the import. The full import block for `oauth.go`:

```go
import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)
```

- [ ] **Step 6: Run all webapi tests**

```bash
go test ./internal/webapi/... -v
```

Expected: all tests pass (`TestSession_*`, `TestRequireSession_*`, `TestMe_*`, `TestOAuth_*`).

- [ ] **Step 7: Commit**

```bash
git add internal/webapi/
git commit -m "feat(webapi): OAuth flow, requireSession middleware, GET /api/me"
```

---

## Task 5: Wire into `serve.go`

**Files:**
- Modify: `cmd/cronfoundry/serve.go`
- Modify: `cmd/cronfoundry/serve_test.go`

- [ ] **Step 1: Write the failing integration test first**

Add to `cmd/cronfoundry/serve_test.go`:

```go
func TestServe_APIMe_WithSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()

	masterKey := mustMasterKey(t)
	t.Setenv(envMasterKey, masterKey)
	t.Setenv(envDatabaseURL, dsn)
	t.Setenv(envGitHubAppID, "42")

	priv, _ := githubtest.MustPrivateKey(t)
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(pemPath, priv, 0o600))
	t.Setenv(envGitHubAppPEM, pemPath)

	t.Setenv(envOAuthClientID, "cid")
	t.Setenv(envOAuthClientSecret, "csec")
	t.Setenv(envAdminLogins, "alice")

	require.NoError(t, runAdminInit(context.Background(), "test-org"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := "127.0.0.1:18090"
	errCh := make(chan error, 1)
	go func() { errCh <- runServe(ctx, addr, 30*time.Second) }()

	// Wait for server.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Craft a valid session cookie manually using the same master key.
	masterBytes, err := secretstore.ParseMasterKey(masterKey)
	require.NoError(t, err)
	cookie, err := webapi.SignSession(
		webapi.SessionClaims{Login: "alice", Role: "admin"},
		masterBytes,
		time.Hour,
	)
	require.NoError(t, err)

	req, _ := http.NewRequest("GET", "http://"+addr+"/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "cf_session", Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)

	var got map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "alice", got["login"])
	assert.Equal(t, "admin", got["role"])

	cancel()
	select {
	case err := <-errCh:
		assert.True(t, err == nil || err == context.Canceled)
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down")
	}
}

func TestServe_MissingOAuthClientID(t *testing.T) {
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, "postgres://example")
	t.Setenv(envGitHubAppID, "42")
	t.Setenv(envGitHubAppPEM, "/tmp/nonexistent.pem")
	t.Setenv(envOAuthClientID, "")
	t.Setenv(envAdminLogins, "alice")
	err := runServe(context.Background(), "127.0.0.1:0", 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envOAuthClientID)
}

func TestServe_MissingAdminLogins(t *testing.T) {
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, "postgres://example")
	t.Setenv(envGitHubAppID, "42")
	t.Setenv(envGitHubAppPEM, "/tmp/nonexistent.pem")
	t.Setenv(envOAuthClientID, "cid")
	t.Setenv(envOAuthClientSecret, "csec")
	t.Setenv(envAdminLogins, "")
	err := runServe(context.Background(), "127.0.0.1:0", 30*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envAdminLogins)
}
```

Also add the new imports to `serve_test.go` (add alongside existing imports):
```go
"encoding/json"

"github.com/gambtho/cronfoundry/internal/secretstore"
"github.com/gambtho/cronfoundry/internal/webapi"
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./cmd/cronfoundry/... -run "TestServe_APIMe|TestServe_MissingOAuth|TestServe_MissingAdmin" -v 2>&1 | head -20
```

Expected: compile error — `envOAuthClientID`, `envAdminLogins`, `webapi` not imported in serve.go.

- [ ] **Step 3: Update `serve.go`**

Add new constants near the top of `cmd/cronfoundry/serve.go` (after the existing `envGitHubAppPEM` line):

```go
const (
	envOAuthClientID     = "CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID"
	envOAuthClientSecret = "CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET"
	envAdminLogins       = "CRONFOUNDRY_ADMIN_LOGINS"
	envViewerLogins      = "CRONFOUNDRY_VIEWER_LOGINS"
)
```

Add validation in `runServe` after the existing GitHub App validation block:

```go
oauthClientID := os.Getenv(envOAuthClientID)
oauthClientSecret := os.Getenv(envOAuthClientSecret)
adminLoginsRaw := os.Getenv(envAdminLogins)
if oauthClientID == "" {
    return fmt.Errorf("%s is required", envOAuthClientID)
}
if oauthClientSecret == "" {
    return fmt.Errorf("%s is required", envOAuthClientSecret)
}
if adminLoginsRaw == "" {
    return fmt.Errorf("%s must contain at least one login", envAdminLogins)
}
adminLogins := splitLogins(adminLoginsRaw)
viewerLogins := splitLogins(os.Getenv(envViewerLogins))
```

Add a helper at the bottom of `serve.go`:

```go
func splitLogins(raw string) []string {
    if raw == "" {
        return nil
    }
    parts := strings.Split(raw, ",")
    out := make([]string, 0, len(parts))
    for _, p := range parts {
        if t := strings.TrimSpace(p); t != "" {
            out = append(out, t)
        }
    }
    return out
}
```

Add `"strings"` to the import block in `serve.go`.

Call `webapi.RegisterRoutes` after building the mux in `runServe`. Replace the `srv := api.NewServer(...)` block:

```go
mux := http.NewServeMux()

// Health check (unauthenticated).
mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
    _, _ = w.Write([]byte("ok"))
})

// Runner-facing API.
api.RegisterRoutes(mux, api.Deps{
    Pool:          pool,
    Signer:        signer,
    Secrets:       store,
    Installations: installs,
})

// Browser-facing API.
webapi.RegisterRoutes(mux, webapi.Deps{
    MasterKey:         master,
    OAuthClientID:     oauthClientID,
    OAuthClientSecret: oauthClientSecret,
    AdminLogins:       adminLogins,
    ViewerLogins:      viewerLogins,
})

srv := &http.Server{Addr: addr, Handler: mux}
```

This requires refactoring `internal/api` to expose `RegisterRoutes` instead of `NewServer`, since the mux is now shared. Update `internal/api/server.go`:

```go
// RegisterRoutes registers all /internal/* routes on mux.
// The caller is responsible for the /healthz route and http.Server construction.
func RegisterRoutes(mux *http.ServeMux, deps Deps) {
    auth := requireBearer(deps.Signer, deps.Pool)

    mux.Handle("GET /internal/runs/{id}/context", auth(runContextHandler{deps}))
    mux.Handle("GET /internal/secrets", auth(secretsHandler{deps}))
    mux.Handle("GET /internal/repos/{id}/clone-url", auth(cloneURLHandler{deps}))
    mux.Handle("POST /internal/runs/{id}/events", auth(eventsHandler{deps}))
    mux.Handle("POST /internal/runs/{id}/finalize", auth(finalizeHandler{deps}))
    mux.Handle("POST /internal/schedules/{id}/run-now", runNowHandler{deps})
}

// NewServer builds an *http.Server with all handlers registered.
// Kept for backwards compatibility with tests that use it directly.
func NewServer(addr string, deps Deps) *http.Server {
    mux := http.NewServeMux()
    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        _, _ = w.Write([]byte("ok"))
    })
    RegisterRoutes(mux, deps)
    return &http.Server{Addr: addr, Handler: mux}
}
```

Add `"github.com/gambtho/cronfoundry/internal/webapi"` to `serve.go` imports.

- [ ] **Step 4: Run all tests**

```bash
go test ./... -short 2>&1 | tail -20
```

Expected: all non-integration tests pass. Integration tests skipped under `-short`.

- [ ] **Step 5: Run integration tests**

```bash
go test ./cmd/cronfoundry/... -run "TestServe" -v -timeout 120s 2>&1 | tail -30
```

Expected: `TestServe_BootsAndHealthz`, `TestServe_APIMe_WithSession` pass; validation tests pass without DB.

- [ ] **Step 6: Commit**

```bash
git add cmd/cronfoundry/serve.go cmd/cronfoundry/serve_test.go internal/api/server.go
git commit -m "feat(serve): wire webapi OAuth + session routes; add env validation"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Covered by |
|---|---|
| GitHub OAuth login/logout flow | Task 4 `oauth.go` |
| Signed session cookies (HMAC-SHA256, master key) | Task 1 `session.go` |
| `requireSession` middleware | Task 2 `auth.go` |
| `GET /api/me` | Task 3 `me.go` |
| Admin/viewer role from env vars | Task 4 `resolveRole`, Task 5 `serve.go` constants |
| Env var validation at startup | Task 5 `serve.go` |
| `HttpOnly`, `SameSite=Lax`, `Secure` when non-localhost | Task 4 `oauth.go` cookie attrs |
| No new DB tables/migrations | Confirmed — none added |
| Unit tests: session, middleware, oauth, me | Tasks 1–4 |
| Integration test: full boot + `/api/me` with cookie | Task 5 `serve_test.go` |
| `webapi` separate from `internal/api` (no circular import) | Task 4 — `Deps` has no `api.Deps` embed |

**Placeholder scan:** No TBDs, no "implement later". ✓

**Type consistency:**
- `SessionClaims` defined in Task 1 — used correctly in Tasks 2, 3, 4. ✓
- `Deps` stub created in Task 3, fully defined in Task 4. ✓
- `oauthHandlers` referenced in Task 3 stub, implemented in Task 4. ✓
- `RegisterRoutes` signature consistent across Tasks 3–5. ✓
- `api.RegisterRoutes` added in Task 5 matches `NewServer` internals. ✓
