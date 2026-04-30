# CSRF Protection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add CSRF protection to all mutating webapi endpoints via a per-session double-submit cookie + `X-CSRF-Token` header pattern, plus an `Origin`/`Referer` allowlist check.

**Architecture:** A new `csrf` middleware in `internal/webapi/csrf.go` wraps `RequireRole` from outside, so CSRF runs before auth — an unauthenticated mutating request gets a CSRF 403 (or origin 403) without paying the cost of session decoding. It rejects mutating requests (POST/PATCH/PUT/DELETE) lacking a matching `cf_csrf` cookie + `X-CSRF-Token` header, or whose `Origin`/`Referer` doesn't match `CRONFOUNDRY_PUBLIC_BASE_URL`. The OAuth callback issues `cf_csrf` alongside `cf_session`; logout clears both. The React SPA reads `cf_csrf` from `document.cookie` in one place (`web/src/lib/api.ts`) and sets the header on every mutating fetch.

**Tech Stack:** Go (`net/http`, `crypto/rand`, `crypto/subtle`), TypeScript/React (vanilla `fetch`), `stretchr/testify` for assertions. No new dependencies.

**Spec:** [`docs/superpowers/specs/2026-04-29-csrf-protection-design.md`](../specs/2026-04-29-csrf-protection-design.md)

## File Map

- **Create** `internal/webapi/csrf.go` — middleware, config struct, token generator helper.
- **Create** `internal/webapi/csrf_test.go` — unit tests for the middleware matrix.
- **Modify** `internal/webapi/oauth.go` — issue `cf_csrf` on callback; clear on logout.
- **Modify** `internal/webapi/oauth_test.go` — assert cookie attributes on callback + logout.
- **Modify** `internal/webapi/server.go` — extend `Deps` with `PublicBaseURL`; wire CSRF into `adminOnly` path.
- **Modify** `internal/webapi/auth.go` — `RequireRole` accepts the CSRF middleware (or we wrap externally; see Task 5).
- **Modify** `cmd/cronfoundry/serve.go` — read `CRONFOUNDRY_PUBLIC_BASE_URL` env var and pass into `Deps`.
- **Modify** `internal/webapi/{repos,schedules,secrets,users,copilot_connect}_test.go` — share helper that injects matching cookie+header on mutating requests.
- **Create** `internal/webapi/testutil_csrf_test.go` — shared `withCSRF` helper for handler tests.
- **Modify** `web/src/lib/api.ts` — read cookie, set header on non-GET requests, redirect on 403 csrf.
- **Create** `web/src/lib/api.test.ts` — verify header injection.
- **Modify** `docs/guides/deploy-azure.md` — document `CRONFOUNDRY_PUBLIC_BASE_URL`.
- **Modify** `deploy/params.example.json` — add new param, plumbed through Bicep.
- **Modify** `deploy/main.bicep` and `deploy/modules/containerApp.bicep` — pass `publicBaseUrl` env var.

---

### Task 1: CSRF middleware — token generator + skeleton

**Files:**
- Create: `internal/webapi/csrf.go`
- Test: `internal/webapi/csrf_test.go`

- [ ] **Step 1: Write failing test for token generator**

```go
package webapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCSRFToken_LengthAndAlphabet(t *testing.T) {
	tok, err := NewCSRFToken()
	require.NoError(t, err)
	// 32 bytes -> 43 chars unpadded base64url
	assert.Len(t, tok, 43)
	assert.False(t, strings.ContainsAny(tok, "+/="), "must be base64url unpadded")
}

func TestNewCSRFToken_UniquePerCall(t *testing.T) {
	a, _ := NewCSRFToken()
	b, _ := NewCSRFToken()
	assert.NotEqual(t, a, b)
}
```

- [ ] **Step 2: Run test, verify fail**

```bash
go test ./internal/webapi/ -run TestNewCSRFToken -v
```
Expected: FAIL `undefined: NewCSRFToken`.

- [ ] **Step 3: Implement token generator**

Create `internal/webapi/csrf.go`:

```go
package webapi

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// CSRFCookieName is the cookie that carries the per-session CSRF token.
// It is non-HttpOnly so the SPA can read it via document.cookie and echo it
// in X-CSRF-Token. The cookie's presence on the cronfoundry origin is the
// security primitive — an attacker on a foreign origin cannot read it.
const CSRFCookieName = "cf_csrf"

// CSRFHeaderName is the header the SPA must set on every state-changing
// request. The middleware compares its value to the cookie with a
// constant-time compare.
const CSRFHeaderName = "X-CSRF-Token"

// NewCSRFToken returns a 32-byte random token, base64url-encoded without
// padding (43 chars). Suitable for use as a per-session CSRF token.
func NewCSRFToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// SetCSRFCookie writes the cf_csrf cookie. Called from the OAuth callback
// alongside SetCookie for cf_session. HttpOnly is intentionally false.
func SetCSRFCookie(w http.ResponseWriter, token string, maxAge int, host string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Secure:   !isLocalhost(host),
	})
}

// ClearCSRFCookie deletes the cf_csrf cookie. Called from logout.
func ClearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CSRFCookieName, MaxAge: -1, Path: "/"})
}
```

- [ ] **Step 4: Run test, verify pass**

```bash
go test ./internal/webapi/ -run TestNewCSRFToken -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/csrf.go internal/webapi/csrf_test.go
git commit -m "csrf: add token generator and cookie helpers"
```

---

### Task 2: CSRF middleware — request validation

**Files:**
- Modify: `internal/webapi/csrf.go`
- Test: `internal/webapi/csrf_test.go`

- [ ] **Step 1: Write the matrix of failing tests**

Append to `internal/webapi/csrf_test.go`:

```go
import (
	"net/http"
	"net/http/httptest"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestCSRF_GETPassesThrough(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCSRF_POST_NoCookie_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "csrf cookie missing")
}

func TestCSRF_POST_CookieNoHeader_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "csrf header missing")
}

func TestCSRF_POST_Mismatch_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "abc"})
	req.Header.Set(CSRFHeaderName, "xyz")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "csrf mismatch")
}

func TestCSRF_POST_Match_NoOriginRequired_OK(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCSRF_POST_OriginMatch_OK(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: "https://cronfoundry.example.com"})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	req.Header.Set("Origin", "https://cronfoundry.example.com")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCSRF_POST_OriginMismatch_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: "https://cronfoundry.example.com"})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "origin mismatch")
}

func TestCSRF_POST_RefererFallback_OK(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: "https://cronfoundry.example.com"})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	req.Header.Set("Referer", "https://cronfoundry.example.com/runs/abc")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCSRF_POST_NoOriginNoReferer_AllowedOriginSet_403(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: "https://cronfoundry.example.com"})(okHandler())
	req := httptest.NewRequest("POST", "/api/repos", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok"})
	req.Header.Set(CSRFHeaderName, "tok")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "origin missing")
}

func TestCSRF_HEADAndOPTIONS_PassThrough(t *testing.T) {
	mw := CSRF(CSRFConfig{AllowedOrigin: ""})(okHandler())
	for _, method := range []string{"HEAD", "OPTIONS"} {
		req := httptest.NewRequest(method, "/api/runs", nil)
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, method)
	}
}
```

- [ ] **Step 2: Run, verify fails**

```bash
go test ./internal/webapi/ -run TestCSRF_ -v
```
Expected: compile errors / `undefined: CSRF, CSRFConfig`.

- [ ] **Step 3: Implement middleware**

Append to `internal/webapi/csrf.go`:

```go
import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
)

// CSRFConfig configures the CSRF middleware.
type CSRFConfig struct {
	// AllowedOrigin is the scheme+host of the trusted public origin
	// (e.g. "https://cronfoundry.example.com"). When empty, the
	// Origin/Referer check is skipped — intended for local dev only.
	AllowedOrigin string
}

// CSRF returns a middleware that enforces double-submit cookie + Origin check
// on all mutating requests (POST, PATCH, PUT, DELETE). GET, HEAD, OPTIONS pass
// through unchanged.
func CSRF(cfg CSRFConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie(CSRFCookieName)
			if err != nil || cookie.Value == "" {
				csrfReject(w, r, "csrf cookie missing")
				return
			}
			header := r.Header.Get(CSRFHeaderName)
			if header == "" {
				csrfReject(w, r, "csrf header missing")
				return
			}
			if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
				csrfReject(w, r, "csrf mismatch")
				return
			}

			if cfg.AllowedOrigin != "" {
				if reason, ok := checkOrigin(r, cfg.AllowedOrigin); !ok {
					csrfReject(w, r, reason)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func checkOrigin(r *http.Request, allowed string) (string, bool) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		ref := r.Header.Get("Referer")
		if ref == "" {
			return "origin missing", false
		}
		u, err := url.Parse(ref)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "origin missing", false
		}
		origin = u.Scheme + "://" + u.Host
	}
	if !strings.EqualFold(origin, allowed) {
		return "origin mismatch", false
	}
	return "", true
}

func csrfReject(w http.ResponseWriter, r *http.Request, reason string) {
	slog.Info("csrf reject", "method", r.Method, "path", r.URL.Path, "reason", reason)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": reason})
}
```

- [ ] **Step 4: Run, verify all pass**

```bash
go test ./internal/webapi/ -run TestCSRF -v
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/csrf.go internal/webapi/csrf_test.go
git commit -m "csrf: middleware with double-submit cookie + Origin check"
```

---

### Task 3: Issue cf_csrf cookie on OAuth callback; clear on logout

**Files:**
- Modify: `internal/webapi/oauth.go`
- Modify: `internal/webapi/oauth_test.go`

- [ ] **Step 1: Write failing tests for callback + logout cookie behavior**

Append to `internal/webapi/oauth_test.go` (assume existing test helpers like `newCallbackRequest` and the deps factory; if not present, mirror the existing `TestOAuth_Callback_*` tests' setup):

```go
func TestOAuth_Callback_SetsCSRFCookie(t *testing.T) {
	// Reuse the existing happy-path callback test setup. The pattern below
	// matches TestOAuth_Callback_* — copy the request build there if this
	// helper doesn't exist.
	w, _ := runHappyPathCallback(t)

	var csrf *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == CSRFCookieName {
			csrf = c
			break
		}
	}
	require.NotNil(t, csrf, "cf_csrf cookie must be set on callback")
	assert.Len(t, csrf.Value, 43, "token must be 43 char base64url")
	assert.False(t, csrf.HttpOnly, "must NOT be HttpOnly so SPA can read it")
	assert.Equal(t, http.SameSiteLaxMode, csrf.SameSite)
	assert.Greater(t, csrf.MaxAge, 0)
}

func TestOAuth_Logout_ClearsCSRFCookie(t *testing.T) {
	deps := Deps{MasterKey: testMasterKey(t)} // adapt to existing helper
	h := oauthHandlers{deps: deps}
	req := httptest.NewRequest("GET", "/oauth/logout", nil)
	w := httptest.NewRecorder()
	h.logout(w, req)

	var found bool
	for _, c := range w.Result().Cookies() {
		if c.Name == CSRFCookieName {
			found = true
			assert.Less(t, c.MaxAge, 0, "must be cleared (MaxAge<0)")
		}
	}
	assert.True(t, found, "logout must emit cf_csrf clear cookie")
}
```

> If `runHappyPathCallback` doesn't exist, find the existing
> `TestOAuth_Callback_Success` test in `oauth_test.go` and either extract a
> helper or inline the same setup in this test. Look for a test that
> exercises the green-path callback and ends with `cf_session` being set.

- [ ] **Step 2: Run, verify fails**

```bash
go test ./internal/webapi/ -run "TestOAuth_(Callback_SetsCSRF|Logout_ClearsCSRF)" -v
```
Expected: FAIL — cookie not present.

- [ ] **Step 3: Modify the callback handler in `internal/webapi/oauth.go`**

Find the block that calls `http.SetCookie(w, &http.Cookie{Name: "cf_session", ...})` (currently around line 140-148) and immediately after it, add:

```go
csrfTok, err := NewCSRFToken()
if err != nil {
	http.Error(w, "csrf token generation failed", http.StatusInternalServerError)
	return
}
SetCSRFCookie(w, csrfTok, int(sessionDuration.Seconds()), r.Host)
```

This must be placed BEFORE `http.Redirect(w, r, "/", http.StatusFound)`.

- [ ] **Step 4: Modify the logout handler in `internal/webapi/oauth.go`**

Find:
```go
func (h oauthHandlers) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "cf_session", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/oauth/login", http.StatusFound)
}
```

Replace with:
```go
func (h oauthHandlers) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "cf_session", MaxAge: -1, Path: "/"})
	ClearCSRFCookie(w)
	http.Redirect(w, r, "/oauth/login", http.StatusFound)
}
```

- [ ] **Step 5: Run, verify pass**

```bash
go test ./internal/webapi/ -run "TestOAuth_" -v
```
Expected: all PASS, including the two new ones.

- [ ] **Step 6: Commit**

```bash
git add internal/webapi/oauth.go internal/webapi/oauth_test.go
git commit -m "csrf: issue cf_csrf cookie on OAuth callback; clear on logout"
```

---

### Task 4: Add `PublicBaseURL` to Deps and wire from server entrypoint

**Files:**
- Modify: `internal/webapi/server.go`
- Modify: `cmd/cronfoundry/serve.go`

- [ ] **Step 1: Add field to Deps**

In `internal/webapi/server.go`, add to the `Deps` struct (in the same block as `WebhookSecret`):

```go
// PublicBaseURL is the externally-reachable base URL of the service
// (scheme+host, e.g. "https://cronfoundry.example.com"). Used for the
// CSRF middleware Origin allowlist. Empty disables the Origin check
// (dev mode).
PublicBaseURL string
```

- [ ] **Step 2: Wire env var in serve.go**

In `cmd/cronfoundry/serve.go`, find where `webapi.Deps{...}` is constructed and the env vars are read (around the `WebhookSecret` setup). Add:

```go
publicBaseURL := os.Getenv("CRONFOUNDRY_PUBLIC_BASE_URL")
```

Pass it into `Deps`:

```go
deps := webapi.Deps{
    // ... existing fields ...
    PublicBaseURL: publicBaseURL,
}
```

Add a non-fatal startup warning when `publicBaseURL` is empty so operators
have a visible signal that the Origin check is disabled:

```go
if publicBaseURL == "" {
    slog.Warn("CRONFOUNDRY_PUBLIC_BASE_URL not set; CSRF Origin check disabled (dev mode)")
}
```

This is intentionally warn-only and unconditional — the cookie+header
double-submit check still runs, the Origin check is the belt-and-suspenders
layer, and operators upgrading from <0.7 shouldn't have their service refuse
to start because of a new env var.

- [ ] **Step 3: Verify compiles**

```bash
go build ./...
```
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/webapi/server.go cmd/cronfoundry/serve.go
git commit -m "csrf: thread CRONFOUNDRY_PUBLIC_BASE_URL into webapi.Deps"
```

---

### Task 5: Wire CSRF into the adminOnly middleware chain

**Files:**
- Modify: `internal/webapi/server.go`
- Test: `internal/webapi/server_test.go` (if exists; otherwise add at end of csrf_test.go)

- [ ] **Step 1: Write a failing wiring test**

Append to `internal/webapi/csrf_test.go`:

```go
func TestRegisterRoutes_AdminMutationsRequireCSRF(t *testing.T) {
	mux := http.NewServeMux()
	deps := Deps{
		MasterKey:     testMasterKey(t),
		PublicBaseURL: "",
	}
	RegisterRoutes(mux, deps)

	// A POST to /api/repos with a valid session but no CSRF should 403.
	req := httptest.NewRequest("POST", "/api/repos", nil)
	// Attach a valid session cookie for an admin login. Reuse whatever
	// helper existing tests use (e.g. signSessionForTest); if missing,
	// duplicate the SignSession call here.
	addAdminSession(t, req, deps.MasterKey)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "csrf cookie missing")
}
```

> `testMasterKey` and `addAdminSession` should already exist in webapi tests
> — search for `MasterKey:` in the existing `_test.go` files to find the
> pattern. If they don't exist as helpers, inline a 32-byte slice and a
> call to `SignSession(SessionClaims{Login: "admin", Role: "admin"}, key, time.Hour)`.

- [ ] **Step 2: Run, verify fails**

```bash
go test ./internal/webapi/ -run TestRegisterRoutes_AdminMutationsRequireCSRF -v
```
Expected: FAIL — request succeeds (or fails for unrelated reason, e.g. nil Queries panic; if so, adjust deps with a nil-tolerant test stub).

- [ ] **Step 3: Modify `RegisterRoutes` to chain CSRF inside adminOnly**

In `internal/webapi/server.go`, replace:

```go
adminOnly := func(h http.Handler) http.Handler {
    return RequireRole(deps.MasterKey, "admin", h)
}
```

with:

```go
csrfMW := CSRF(CSRFConfig{AllowedOrigin: deps.PublicBaseURL})
adminOnly := func(h http.Handler) http.Handler {
    return csrfMW(RequireRole(deps.MasterKey, "admin", h))
}
```

The order matters: outer-to-inner is `csrfMW → RequireRole → handler`. CSRF runs first — an unauthenticated mutating request without a matching cookie+header gets a 403 csrf before paying any session-decoding cost. The handler runs only when both CSRF and auth pass.

- [ ] **Step 4: Run, verify pass**

```bash
go test ./internal/webapi/ -run TestRegisterRoutes_AdminMutationsRequireCSRF -v
```
Expected: PASS.

- [ ] **Step 5: Run full webapi suite — many existing tests will fail**

```bash
go test ./internal/webapi/ -v 2>&1 | tail -40
```
Expected: existing handler tests (`secrets_test.go`, `users_test.go`, etc.) FAIL with 403 csrf — this is correct; Task 6 fixes them.

- [ ] **Step 6: Commit**

```bash
git add internal/webapi/server.go internal/webapi/csrf_test.go
git commit -m "csrf: chain middleware inside adminOnly (failing existing tests)"
```

---

### Task 6: Update existing handler tests with shared CSRF helper

**Files:**
- Create: `internal/webapi/testutil_csrf_test.go`
- Modify: `internal/webapi/{repos,schedules,secrets,users,copilot_connect,schedule_overrides}_test.go`

- [ ] **Step 1: Create the shared helper**

Create `internal/webapi/testutil_csrf_test.go`:

```go
package webapi

import (
	"net/http"
	"testing"
)

// withCSRF attaches a matching cf_csrf cookie + X-CSRF-Token header to req.
// All handler tests that exercise mutating endpoints must call this on the
// request before serving it through the mux/middleware.
func withCSRF(_ *testing.T, req *http.Request) *http.Request {
	const tok = "test-csrf-token-value"
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: tok})
	req.Header.Set(CSRFHeaderName, tok)
	return req
}
```

- [ ] **Step 2: Identify failing tests**

```bash
go test ./internal/webapi/ 2>&1 | grep -E "^--- FAIL|FAIL:" | head -40
```

Expected: a list. For each failing test that does a mutating request, find the line that builds the request (e.g. `req := httptest.NewRequest("POST", "/api/repos", body)`) and immediately after, insert:

```go
req = withCSRF(t, req)
```

Files to inspect:
- `internal/webapi/repos_test.go`
- `internal/webapi/schedules_test.go`
- `internal/webapi/schedule_overrides_test.go`
- `internal/webapi/secrets_test.go`
- `internal/webapi/users_test.go`
- `internal/webapi/copilot_connect_test.go`
- `internal/webapi/audit_test.go` (if it exercises mutations)

> Walk each file: `grep -n 'NewRequest("POST\|PATCH\|PUT\|DELETE' internal/webapi/<file>_test.go`. For every match, insert the `withCSRF(t, req)` call immediately after the request is built but before it's served.

- [ ] **Step 3: Run all tests**

```bash
go test ./internal/webapi/ -v 2>&1 | tail -40
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/webapi/testutil_csrf_test.go internal/webapi/*_test.go
git commit -m "csrf: update handler tests to attach matching cookie+header"
```

---

### Task 7: Front-end — read cookie, set X-CSRF-Token header, handle 403

**Files:**
- Modify: `web/src/lib/api.ts`
- Create: `web/src/lib/api.test.ts`

- [ ] **Step 1: Write failing front-end test**

Create `web/src/lib/api.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'

describe('apiFetch CSRF behavior', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => 'cf_csrf=tok123; cf_session=sig.payload',
    })
    fetchSpy = vi.spyOn(global, 'fetch').mockResolvedValue(
      new Response('null', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('sets X-CSRF-Token on POST', async () => {
    const { api } = await import('./api')
    await api.repos.connect({ install_id: 1, owner: 'a', name: 'b' })
    const init = fetchSpy.mock.calls[0][1] as RequestInit
    const headers = new Headers(init.headers)
    expect(headers.get('X-CSRF-Token')).toBe('tok123')
  })

  it('omits X-CSRF-Token on GET', async () => {
    const { api } = await import('./api')
    await api.repos.list()
    const init = fetchSpy.mock.calls[0][1] as RequestInit
    const headers = new Headers(init?.headers)
    expect(headers.get('X-CSRF-Token')).toBeNull()
  })
})
```

- [ ] **Step 2: Run, verify fails**

```bash
cd web && npm test -- api.test
```
Expected: FAIL — `X-CSRF-Token` not set.

- [ ] **Step 3: Modify `web/src/lib/api.ts`**

Replace the `apiFetch` function with:

```ts
function csrfToken(): string | undefined {
  const m = document.cookie.split('; ').find(c => c.startsWith('cf_csrf='))
  return m ? m.slice('cf_csrf='.length) : undefined
}

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? 'GET').toUpperCase()
  const headers = new Headers(init?.headers)
  if (!SAFE_METHODS.has(method)) {
    const t = csrfToken()
    if (t) headers.set('X-CSRF-Token', t)
  }
  const res = await fetch(path, { credentials: 'include', ...init, headers })
  if (res.status === 401) {
    window.location.href = '/oauth/login'
    return Promise.reject(new Error('unauthorized'))
  }
  if (res.status === 403) {
    const body = await res.clone().json().catch(() => ({}))
    if (typeof body.error === 'string' && body.error.startsWith('csrf')) {
      window.location.href = '/oauth/login'
      return Promise.reject(new Error('csrf rejected; re-authenticating'))
    }
    // fall through — non-csrf 403 is a real authz failure
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error((body as { error?: string }).error ?? res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}
```

- [ ] **Step 4: Run, verify pass**

```bash
cd web && npm test -- api.test && npm run typecheck && npm run lint
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api.ts web/src/lib/api.test.ts
git commit -m "csrf: SPA reads cf_csrf cookie and sets X-CSRF-Token on mutations"
```

---

### Task 8: Document the new env var in deploy guide and Bicep params

**Files:**
- Modify: `docs/guides/deploy-azure.md`
- Modify: `deploy/params.example.json`
- Modify: `deploy/main.bicep`
- Modify: `deploy/modules/containerApp.bicep`

- [ ] **Step 1: Add `publicBaseUrl` Bicep param**

In `deploy/main.bicep`, add to the parameter block (alongside existing `imageTag`, `adminLogins`, etc.):

```bicep
@description('Externally-reachable base URL of the service (scheme+host). Required in production for CSRF Origin check.')
param publicBaseUrl string = ''
```

Plumb it to the `containerApp` module call:

```bicep
module containerApp 'modules/containerApp.bicep' = {
  ...
  params: {
    ...
    publicBaseUrl: publicBaseUrl
  }
}
```

- [ ] **Step 2: Add to `deploy/modules/containerApp.bicep`**

Add a parameter:
```bicep
param publicBaseUrl string = ''
```

In the env var array for the container, append:
```bicep
{
  name: 'CRONFOUNDRY_PUBLIC_BASE_URL'
  value: publicBaseUrl
}
```

- [ ] **Step 3: Update `deploy/params.example.json`**

Add a new entry alongside existing params:
```json
"publicBaseUrl": {
  "value": "https://cronfoundry.example.com"
}
```

- [ ] **Step 4: Document in `docs/guides/deploy-azure.md`**

Find the env-var / params table (or the §3 "Configure parameters" section). Add a row:

| Param | Required | Notes |
|---|---|---|
| `publicBaseUrl` | yes (prod) | Externally-reachable URL of the Container App, e.g. `https://cronfoundry.example.com`. Used for CSRF `Origin` check. Empty disables the check (local dev only). |

- [ ] **Step 5: Verify Bicep compiles**

```bash
cd deploy && az bicep build --file main.bicep --stdout > /dev/null
```
Expected: clean. (Skip if `az` not installed locally; CI catches it.)

- [ ] **Step 6: Commit**

```bash
git add docs/guides/deploy-azure.md deploy/params.example.json deploy/main.bicep deploy/modules/containerApp.bicep
git commit -m "csrf: document and plumb CRONFOUNDRY_PUBLIC_BASE_URL through deploy"
```

---

### Task 9: Reference spec from PRD; mention in release notes / README

**Files:**
- Modify: `docs/superpowers/specs/2026-04-19-cronfoundry-prd.md`
- Modify: `README.md`

- [ ] **Step 1: Annotate PRD NFR-2.4**

In `docs/superpowers/specs/2026-04-19-cronfoundry-prd.md`, find:
```text
- NFR-2.4 CSRF protection on all mutating endpoints.
```

Replace with:
```text
- NFR-2.4 CSRF protection on all mutating endpoints. **Implementation:**
  see [`2026-04-29-csrf-protection-design.md`](./2026-04-29-csrf-protection-design.md).
```

- [ ] **Step 2: Add a paragraph to README's Operator endpoints / setup section**

In `README.md`, find the "Operator endpoints" section and add a note above or below it:

```markdown
### CSRF & origin allowlist

Set `CRONFOUNDRY_PUBLIC_BASE_URL` to the externally-reachable URL of the
service (scheme+host). The CSRF middleware uses this as the allowlist for
the `Origin`/`Referer` check. In dev (no env var), the origin check is
disabled; the cookie+header check still runs.
```

- [ ] **Step 3: Run final full test suite**

```bash
make lint && go test ./... -timeout 10m && cd web && npm run lint && npm run typecheck && npm test
```
Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/superpowers/specs/2026-04-19-cronfoundry-prd.md
git commit -m "docs: reference CSRF design from PRD and README"
```

---

### Task 10: Open the PR

- [ ] **Step 1: Push branch**

```bash
git push -u origin worktree-spec-csrf
```

- [ ] **Step 2: Open PR**

```bash
gh pr create --title "feat(security): CSRF protection via double-submit cookie + Origin check" --body "$(cat <<'EOF'
## Summary

Implements [CSRF design](docs/superpowers/specs/2026-04-29-csrf-protection-design.md) closing PRD NFR-2.4.

- New `csrf` middleware (double-submit cookie + Origin/Referer allowlist) chained inside `adminOnly`
- `cf_csrf` cookie issued on OAuth callback, cleared on logout
- React SPA reads cookie and sets `X-CSRF-Token` on every non-GET request
- `CRONFOUNDRY_PUBLIC_BASE_URL` env var threaded through Bicep

## Operator note

Existing live sessions will receive a 403 on their next mutation and the SPA will redirect to login — re-auth issues both `cf_session` and `cf_csrf`. Document this in the next release notes.

## Test plan

- [ ] `go test ./internal/webapi/...` green
- [ ] `cd web && npm test && npm run lint && npm run typecheck` green
- [ ] Manual: log in, trigger run-now, rotate a secret, delete a user — each should succeed
- [ ] Manual: with browser devtools, delete cf_csrf cookie and try a mutation — should 403 and redirect to login
EOF
)"
```

- [ ] **Step 3: Report PR URL**

---

## Self-review

**Spec coverage:**

- Cookie issuance on callback ✅ Task 3
- Cookie clear on logout ✅ Task 3
- Middleware with method gate, cookie/header compare, Origin check ✅ Tasks 1, 2
- Wiring inside `adminOnly` ✅ Task 5
- Front-end header injection ✅ Task 7
- Front-end 403 → re-auth ✅ Task 7
- Tests for the matrix ✅ Task 2 (matrix), 3 (cookie attrs), 5 (wiring), 6 (handler regression), 7 (SPA)
- `CRONFOUNDRY_PUBLIC_BASE_URL` env var ✅ Task 4, 8
- Docs (deploy guide + params + README + PRD pointer) ✅ Tasks 8, 9
- `/webhook/github` excluded ✅ correct (it doesn't go through `adminOnly`)
- No DB migration ✅ confirmed
- Constant-time compare ✅ Task 2 (`subtle.ConstantTimeCompare`)

**Placeholder scan:** No "TBD" / "fill in details". Each step has the actual code. Two minor judgment calls flagged with quoted-block notes for the implementer (test-helper reuse in Task 3 step 1; serve.go env-var validation pattern in Task 4 step 2). These are intentional — the implementer needs to inspect existing patterns rather than have me invent new ones.

**Type consistency:** `CSRFCookieName`, `CSRFHeaderName`, `NewCSRFToken`, `SetCSRFCookie`, `ClearCSRFCookie`, `CSRF`, `CSRFConfig` — used consistently across tasks. `withCSRF` helper signature stable across all handler tests.

**Spec rollout flag:** The spec mentioned a possible `CRONFOUNDRY_CSRF_ENFORCE` flag for graceful rollout. The plan **does not** ship this flag — instead it relies on the SPA's 403→login redirect, which gives a clean re-auth path without an operator-side toggle. That matches the spec's "decision deferred to implementation; for a self-hosted single-tenant tool, just shipping enforced is also fine."
