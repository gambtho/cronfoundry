# Rate Limiting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-IP rate limiting to publicly reachable HTTP routes (`/api/*`, `/oauth/*`, `/webhook/github`) plus a per-IP concurrent-stream cap on the SSE endpoint, all in-process and configured via env vars.

**Architecture:** A single `RateLimiter` type in `internal/webapi/ratelimit.go` owns one `groupBucket` per route group (`api`, `oauth`, `webhook`). Each bucket is an LRU map of IP → `*rate.Limiter`. SSE concurrency is tracked separately via a `sync.Map` keyed by IP. Two middlewares — `Group(name, h)` and `SSE(h)` — wrap routes in `RegisterRoutes`. IP source is `r.RemoteAddr`, switching to leftmost `X-Forwarded-For` only when `CRONFOUNDRY_TRUST_PROXY=true`.

**Tech Stack:** Go (`golang.org/x/time/rate`, `github.com/hashicorp/golang-lru/v2`, `net/http`, `sync`), `stretchr/testify`. One new direct dep: `golang-lru/v2`.

**Spec:** [`docs/superpowers/specs/2026-04-29-rate-limiting-design.md`](../specs/2026-04-29-rate-limiting-design.md)

## File Map

- **Create** `internal/webapi/ratelimit.go` — `RateLimiter`, `RateLimiterConfig`, `groupBucket`, `Group`, `SSE`, `clientIP` helper.
- **Create** `internal/webapi/ratelimit_test.go` — full test matrix.
- **Modify** `internal/webapi/server.go` — extend `Deps` with `RateLimit RateLimiterConfig`; build `RateLimiter` in `RegisterRoutes`; wrap `session`, `adminOnly`, the OAuth routes, the webhook route, and the SSE route.
- **Modify** `cmd/cronfoundry/serve.go` — read all `CRONFOUNDRY_RATE_*` env vars and `CRONFOUNDRY_TRUST_PROXY`; warn if `PUBLIC_BASE_URL` is set but `TRUST_PROXY` is not.
- **Modify** `go.mod` / `go.sum` — add `github.com/hashicorp/golang-lru/v2`.
- **Modify** `docs/guides/deploy-azure.md` — document new env vars.
- **Modify** `deploy/params.example.json` + `deploy/main.bicep` + `deploy/modules/containerApp.bicep` — pass `trustProxy` and rate-limit overrides through (only `trustProxy` as a Bicep param; the RPM tunables stay env-var-only — operators rarely change them, no need for Bicep surface area).

---

### Task 1: Add the `golang-lru/v2` dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add dependency**

```bash
cd /home/tng/workspace/cronfoundry/.claude/worktrees/spec-ratelimit
go get github.com/hashicorp/golang-lru/v2@latest
go mod tidy
```

Expected: `go.mod` gains a line for `github.com/hashicorp/golang-lru/v2 vX.Y.Z`. `go.sum` gains hashes.

- [ ] **Step 2: Verify**

```bash
go build ./...
```

Expected: clean build.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "ratelimit: add golang-lru/v2 dependency"
```

---

### Task 2: `clientIP` helper and tests

**Files:**
- Create: `internal/webapi/ratelimit.go`
- Test: `internal/webapi/ratelimit_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/webapi/ratelimit_test.go`:

```go
package webapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientIP_NoTrustProxy_UsesRemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	assert.Equal(t, "10.0.0.5", clientIP(req, false))
}

func TestClientIP_TrustProxy_PrefersLeftmostXFF(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	assert.Equal(t, "1.2.3.4", clientIP(req, true))
}

func TestClientIP_TrustProxy_FallbackWhenXFFMissing(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	assert.Equal(t, "10.0.0.5", clientIP(req, true))
}

func TestClientIP_TrustProxy_FallbackWhenXFFMalformed(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	req.Header.Set("X-Forwarded-For", "  ")
	assert.Equal(t, "10.0.0.5", clientIP(req, true))
}

func TestClientIP_RemoteAddrWithoutPort(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "10.0.0.5"
	assert.Equal(t, "10.0.0.5", clientIP(req, false))
}
```

- [ ] **Step 2: Run test, verify FAIL**

```bash
go test ./internal/webapi/ -run TestClientIP -v
```

Expected: `undefined: clientIP`.

- [ ] **Step 3: Implement helper**

Create `internal/webapi/ratelimit.go`:

```go
package webapi

import (
	"net"
	"net/http"
	"strings"
)

// clientIP returns the request's client IP. When trustProxy is true, it
// honors the leftmost entry of X-Forwarded-For; otherwise it always uses
// r.RemoteAddr. Falls back to RemoteAddr if XFF is missing or malformed.
// Strips ports and trims whitespace; never returns "".
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			leftmost := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
			if leftmost != "" {
				return leftmost
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
```

- [ ] **Step 4: Run test, verify PASS**

```bash
go test ./internal/webapi/ -run TestClientIP -v
```

Expected: 5 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/ratelimit.go internal/webapi/ratelimit_test.go
git commit -m "ratelimit: add clientIP helper with X-Forwarded-For support"
```

---

### Task 3: `RateLimiter` skeleton + `Group` middleware

**Files:**
- Modify: `internal/webapi/ratelimit.go`
- Modify: `internal/webapi/ratelimit_test.go`

- [ ] **Step 1: Write failing tests for Group middleware**

Append to `ratelimit_test.go`:

```go
import (
	// ... existing imports plus:
	"sync/atomic"
)

func okHandlerCount(count *int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(count, 1)
		w.WriteHeader(http.StatusOK)
	})
}

func newTestRL(t *testing.T, cfg RateLimiterConfig) *RateLimiter {
	t.Helper()
	rl, err := NewRateLimiter(cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	return rl
}

func TestRateLimiter_AllowsBurstThenRejects(t *testing.T) {
	cfg := RateLimiterConfig{
		APIRPM:           60, // burst derived = max(60/6, 3) = 10
		OAuthRPM:         10,
		WebhookRPM:       300,
		SSEMaxConcurrent: 5,
		LRUSize:          128,
	}
	rl := newTestRL(t, cfg)
	var count int64
	mw := rl.Group("api", okHandlerCount(&count))

	// 10 requests should all pass (burst)
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d", i)
	}

	// 11th request should 429
	req := httptest.NewRequest("GET", "/api/x", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "1", w.Header().Get("Retry-After"))
	assert.Contains(t, w.Body.String(), "rate limited")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, int64(10), atomic.LoadInt64(&count), "downstream handler called only 10 times")
}

func TestRateLimiter_PerIPIsolation(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128})
	var count int64
	mw := rl.Group("api", okHandlerCount(&count))

	// Drain IP A
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		_ = w
	}

	// IP B still allowed
	req := httptest.NewRequest("GET", "/api/x", nil)
	req.RemoteAddr = "2.2.2.2:1"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimiter_PerGroupIsolation(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128})
	api := rl.Group("api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	oauth := rl.Group("oauth", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	// Drain api for IP A (burst = 10)
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		api.ServeHTTP(w, req)
	}

	// Same IP, oauth group, still allowed
	req := httptest.NewRequest("GET", "/oauth/login", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w := httptest.NewRecorder()
	oauth.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimiter_Disabled_PassesThrough(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{
		Disabled:         true,
		APIRPM:           60,
		OAuthRPM:         10,
		WebhookRPM:       300,
		SSEMaxConcurrent: 5,
		LRUSize:          128,
	})
	mw := rl.Group("api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for i := 0; i < 200; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d", i)
	}
}

func TestRateLimiter_RPMZero_DisablesGroup(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 0, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128})
	mw := rl.Group("api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "request %d", i)
	}
}

func TestRateLimiter_LRU_EvictionGivesFreshBudget(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 2})
	mw := rl.Group("api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))

	// Drain IP A's burst.
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}

	// Touch IP B and IP C to evict A from a 2-entry LRU.
	for _, ip := range []string{"2.2.2.2:1", "3.3.3.3:1"} {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}

	// IP A returns and gets a fresh limiter (200, not 429).
	req := httptest.NewRequest("GET", "/api/x", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
```

- [ ] **Step 2: Run, expect compile errors**

```bash
go test ./internal/webapi/ -run TestRateLimiter -v
```

Expected: `undefined: RateLimiter, RateLimiterConfig, NewRateLimiter`.

- [ ] **Step 3: Implement the type**

Append to `internal/webapi/ratelimit.go`:

```go
import (
	// ... existing imports plus:
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/time/rate"
)

// RateLimiterConfig holds the operator-tunable rate-limit knobs. Zero RPM
// for any group disables that group; Disabled=true disables the entire
// middleware (kill switch).
type RateLimiterConfig struct {
	TrustProxy       bool
	Disabled         bool
	APIRPM           int
	OAuthRPM         int
	WebhookRPM       int
	SSEMaxConcurrent int
	LRUSize          int
}

// RateLimiter holds per-group token-bucket state plus an SSE concurrency
// counter. Construct with NewRateLimiter. The Group and SSE methods
// return middlewares.
type RateLimiter struct {
	cfg           RateLimiterConfig
	groups        map[string]*groupBucket
	sseConcurrent sync.Map // ip → *int64
}

type groupBucket struct {
	limit rate.Limit
	burst int
	mu    sync.Mutex
	lru   *lru.Cache[string, *rate.Limiter]
}

// NewRateLimiter returns a RateLimiter. Returns an error only on bad
// config (e.g., LRUSize < 1).
func NewRateLimiter(cfg RateLimiterConfig) (*RateLimiter, error) {
	if cfg.LRUSize < 1 {
		cfg.LRUSize = 4096
	}
	rl := &RateLimiter{cfg: cfg, groups: make(map[string]*groupBucket, 3)}
	for _, g := range []struct {
		name string
		rpm  int
	}{
		{"api", cfg.APIRPM},
		{"oauth", cfg.OAuthRPM},
		{"webhook", cfg.WebhookRPM},
	} {
		if g.rpm <= 0 {
			rl.groups[g.name] = nil // sentinel: group disabled
			continue
		}
		burst := g.rpm / 6
		if burst < 3 {
			burst = 3
		}
		c, err := lru.New[string, *rate.Limiter](cfg.LRUSize)
		if err != nil {
			return nil, fmt.Errorf("ratelimit: lru for %s: %w", g.name, err)
		}
		rl.groups[g.name] = &groupBucket{
			limit: rate.Limit(float64(g.rpm) / 60.0),
			burst: burst,
			lru:   c,
		}
	}
	return rl, nil
}

// Group returns a middleware that applies the named group's per-IP token
// bucket. Unknown group name panics — caller bug.
func (rl *RateLimiter) Group(name string, next http.Handler) http.Handler {
	if rl.cfg.Disabled {
		return next
	}
	b, ok := rl.groups[name]
	if !ok {
		panic(fmt.Sprintf("ratelimit: unknown group %q", name))
	}
	if b == nil {
		// Group explicitly disabled (RPM=0).
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, rl.cfg.TrustProxy)
		lim := b.limiterFor(ip)
		if !lim.Allow() {
			rejectRateLimit(w, r, name, ip, "1")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (b *groupBucket) limiterFor(ip string) *rate.Limiter {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lim, ok := b.lru.Get(ip); ok {
		return lim
	}
	lim := rate.NewLimiter(b.limit, b.burst)
	b.lru.Add(ip, lim)
	return lim
}

func rejectRateLimit(w http.ResponseWriter, r *http.Request, group, ip, retryAfter string) {
	slog.Info("ratelimit reject", "group", group, "ip", ip, "method", r.Method, "path", r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", retryAfter)
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate limited"})
}
```

The new imports go into the existing import block from Task 2 (single `import (...)` block per Go file).

- [ ] **Step 4: Run, verify all PASS**

```bash
go test ./internal/webapi/ -run TestRateLimiter -v
```

Expected: 6 PASS.

```bash
go vet ./...
```

Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/ratelimit.go internal/webapi/ratelimit_test.go
git commit -m "ratelimit: per-IP token bucket Group middleware"
```

---

### Task 4: SSE concurrency-cap middleware

**Files:**
- Modify: `internal/webapi/ratelimit.go`
- Modify: `internal/webapi/ratelimit_test.go`

- [ ] **Step 1: Write failing test**

Append to `ratelimit_test.go`:

```go
func TestRateLimiter_SSE_ConcurrencyCap(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 5, LRUSize: 128})

	// Block until released — simulates an open SSE stream.
	release := make(chan struct{})
	mw := rl.SSE(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		<-release
	}))

	// Open 5 concurrent streams from the same IP.
	done := make(chan int, 6)
	for i := 0; i < 5; i++ {
		go func() {
			req := httptest.NewRequest("GET", "/api/runs/x/events/stream", nil)
			req.RemoteAddr = "1.1.1.1:1"
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)
			done <- w.Code
		}()
	}

	// 6th request from same IP should 429 immediately.
	time.Sleep(50 * time.Millisecond) // let the 5 register
	req := httptest.NewRequest("GET", "/api/runs/x/events/stream", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "5", w.Header().Get("Retry-After"))

	// Release one of the 5; another from same IP now succeeds.
	close(release)
	for i := 0; i < 5; i++ {
		<-done
	}

	req = httptest.NewRequest("GET", "/api/runs/x/events/stream", nil)
	req.RemoteAddr = "1.1.1.1:1"
	w = httptest.NewRecorder()
	mw.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRateLimiter_SSE_PerIPIsolation(t *testing.T) {
	rl := newTestRL(t, RateLimiterConfig{APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300, SSEMaxConcurrent: 1, LRUSize: 128})

	release := make(chan struct{})
	mw := rl.SSE(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		<-release
	}))

	// IP A occupies the only slot.
	go func() {
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = "1.1.1.1:1"
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
	}()
	time.Sleep(50 * time.Millisecond)

	// IP B has its own slot.
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "2.2.2.2:1"
	w := httptest.NewRecorder()
	go func() { mw.ServeHTTP(w, req) }()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, http.StatusOK, w.Code)

	close(release)
}
```

You'll need to add `"time"` to the imports in `ratelimit_test.go`.

- [ ] **Step 2: Run, verify FAIL**

```bash
go test ./internal/webapi/ -run TestRateLimiter_SSE -v
```

Expected: `undefined: rl.SSE`.

- [ ] **Step 3: Implement SSE middleware**

Append to `internal/webapi/ratelimit.go`:

```go
// SSE returns a middleware enforcing a per-IP concurrent-stream cap.
// Increments at request start, decrements when the handler returns.
// Uses a 5-second Retry-After to discourage browser reconnect storms.
func (rl *RateLimiter) SSE(next http.Handler) http.Handler {
	if rl.cfg.Disabled || rl.cfg.SSEMaxConcurrent <= 0 {
		return next
	}
	cap64 := int64(rl.cfg.SSEMaxConcurrent)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, rl.cfg.TrustProxy)
		counterAny, _ := rl.sseConcurrent.LoadOrStore(ip, new(int64))
		counter := counterAny.(*int64)
		// Reserve a slot atomically. If we'd exceed the cap, undo and reject.
		if v := atomicAdd64(counter, 1); v > cap64 {
			atomicAdd64(counter, -1)
			rejectRateLimit(w, r, "sse", ip, "5")
			return
		}
		defer atomicAdd64(counter, -1)
		next.ServeHTTP(w, r)
	})
}

// atomicAdd64 is a thin wrapper around atomic.AddInt64 that returns the
// new value. Indirection lets the test suite replace it for failure
// injection if needed; production callers can ignore.
func atomicAdd64(p *int64, delta int64) int64 {
	return atomicAddInt64(p, delta)
}
```

Add to imports: `"sync/atomic"`. Then alias the wrapper in the same file (single declaration):

Actually, simplify — drop the wrapper and use `atomic.AddInt64` directly:

```go
func (rl *RateLimiter) SSE(next http.Handler) http.Handler {
	if rl.cfg.Disabled || rl.cfg.SSEMaxConcurrent <= 0 {
		return next
	}
	cap64 := int64(rl.cfg.SSEMaxConcurrent)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r, rl.cfg.TrustProxy)
		counterAny, _ := rl.sseConcurrent.LoadOrStore(ip, new(int64))
		counter := counterAny.(*int64)
		if v := atomic.AddInt64(counter, 1); v > cap64 {
			atomic.AddInt64(counter, -1)
			rejectRateLimit(w, r, "sse", ip, "5")
			return
		}
		defer atomic.AddInt64(counter, -1)
		next.ServeHTTP(w, r)
	})
}
```

Add `"sync/atomic"` to the import block. Remove the wrapper variants from the snippet above.

- [ ] **Step 4: Run, verify PASS**

```bash
go test ./internal/webapi/ -run TestRateLimiter_SSE -v
go vet ./...
```

Expected: 2 PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/ratelimit.go internal/webapi/ratelimit_test.go
git commit -m "ratelimit: per-IP SSE concurrency cap"
```

---

### Task 5: Add `RateLimit` to Deps; build limiter in `RegisterRoutes`; wire all routes

**Files:**
- Modify: `internal/webapi/server.go`
- Modify: `internal/webapi/ratelimit_test.go`

- [ ] **Step 1: Add a wiring test**

Append to `ratelimit_test.go`:

```go
func TestRegisterRoutes_OAuthLoginRateLimited(t *testing.T) {
	mux := http.NewServeMux()
	deps := Deps{
		MasterKey: bytes.Repeat([]byte("k"), 32),
		RateLimit: RateLimiterConfig{
			APIRPM: 60, OAuthRPM: 10, WebhookRPM: 300,
			SSEMaxConcurrent: 5, LRUSize: 128,
		},
	}
	RegisterRoutes(mux, deps)

	// oauth burst = max(10/6, 3) = 3. Fire 3 then expect 429 on the 4th.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/oauth/login", nil)
		req.RemoteAddr = "9.9.9.9:1"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusTooManyRequests, w.Code, "request %d", i)
	}
	req := httptest.NewRequest("GET", "/oauth/login", nil)
	req.RemoteAddr = "9.9.9.9:1"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}
```

`bytes` import will need adding if not already in the test file (after Task 3 it should be there for the CSRF wiring test that already exists; verify).

- [ ] **Step 2: Run, expect FAIL**

```bash
go test ./internal/webapi/ -run TestRegisterRoutes_OAuthLoginRateLimited -v
```

Expected: 200 instead of 429 (rate limit not wired).

- [ ] **Step 3: Add RateLimit field to Deps**

In `internal/webapi/server.go`, find the `Deps` struct (just below the `PublicBaseURL` field added in the CSRF work). Append a new field:

```go
// RateLimit configures per-IP rate limiting. Zero values disable each
// group individually; Disabled=true disables the whole middleware.
RateLimit RateLimiterConfig
```

- [ ] **Step 4: Build limiter and wrap routes**

Replace the body of `RegisterRoutes` from `mux.Handle("GET /api/me", ...)` through `mux.Handle("POST /webhook/github", wh)` with versions wrapped in rate-limit middleware. Specifically:

Before the existing `session := func(...)` and `adminOnly := func(...)` definitions, add:

```go
rl, err := NewRateLimiter(deps.RateLimit)
if err != nil {
    panic(fmt.Sprintf("webapi: rate limiter init: %v", err))
}
```

Add `"fmt"` to the import block if not already there.

Replace `session` and `adminOnly` with the rate-limited versions:

```go
session := func(h http.Handler) http.Handler {
    return rl.Group("api", RequireSession(deps.MasterKey, h))
}
csrfMW := CSRF(CSRFConfig{AllowedOrigin: deps.PublicBaseURL})
adminOnly := func(h http.Handler) http.Handler {
    return rl.Group("api", csrfMW(RequireRole(deps.MasterKey, "admin", h)))
}
```

(That moves the `csrfMW := ...` line up by a few lines to keep adminOnly's body single-line; if you'd rather keep it where it is, leave the existing csrfMW declaration alone and reference it from the new adminOnly.)

For OAuth routes (currently `mux.HandleFunc`), change to `mux.Handle` with the wrapper:

```go
mux.Handle("GET /oauth/login", rl.Group("oauth", http.HandlerFunc(oh.login)))
mux.Handle("GET /oauth/callback", rl.Group("oauth", http.HandlerFunc(oh.callback)))
mux.HandleFunc("GET /oauth/logout", oh.logout) // intentionally not rate limited
```

Webhook:

```go
mux.Handle("POST /webhook/github", rl.Group("webhook", wh))
```

SSE — wrap with `rl.SSE` *inside* `session()`:

```go
mux.Handle("GET /api/runs/{id}/events/stream", session(rl.SSE(http.HandlerFunc(evh.stream))))
```

The other `/api/runs/{id}/events` (non-stream) stays as-is — only the stream endpoint gets the SSE concurrency cap.

- [ ] **Step 5: Run new test + full webapi suite**

```bash
go test ./internal/webapi/ -run TestRegisterRoutes_OAuthLoginRateLimited -v
go test -short ./internal/webapi/ 2>&1 | tail -10
go vet ./...
```

Expected: new test PASS; full suite PASS (no regressions); vet clean.

If the full suite has any new 429s, that's a test using a hot-loop on a single IP. Check whether any existing test issues 11+ requests rapidly — if so, that test should set `Disabled: true` on its `RateLimit` config or use distinct `RemoteAddr` values.

- [ ] **Step 6: Commit**

```bash
git add internal/webapi/server.go internal/webapi/ratelimit_test.go
git commit -m "ratelimit: wire middleware into all public route groups"
```

---

### Task 6: Read env vars in `cmd/cronfoundry/serve.go`

**Files:**
- Modify: `cmd/cronfoundry/serve.go`

- [ ] **Step 1: Add env-var constants**

In `cmd/cronfoundry/serve.go`, near the existing `env*` constants block (around line 40-50, look for `envWebhookSecret`, `envPublicBaseURL`), add:

```go
envTrustProxy        = "CRONFOUNDRY_TRUST_PROXY"
envRateAPIRPM        = "CRONFOUNDRY_RATE_API_RPM"
envRateOAuthRPM      = "CRONFOUNDRY_RATE_OAUTH_RPM"
envRateWebhookRPM    = "CRONFOUNDRY_RATE_WEBHOOK_RPM"
envRateSSEConcurrent = "CRONFOUNDRY_RATE_SSE_MAX_CONCURRENT"
envRateLRUSize       = "CRONFOUNDRY_RATE_LRU_SIZE"
envRateDisabled      = "CRONFOUNDRY_RATE_DISABLED"
```

- [ ] **Step 2: Add a helper to parse int env vars with defaults**

Look for an existing parse-int helper in `cmd/cronfoundry/serve.go`. If none, add:

```go
func envInt(name string, def int) int {
    if v := os.Getenv(name); v != "" {
        if i, err := strconv.Atoi(v); err == nil {
            return i
        }
    }
    return def
}

func envBool(name string) bool {
    v := strings.ToLower(os.Getenv(name))
    return v == "1" || v == "true" || v == "yes"
}
```

Add `"strconv"` and `"strings"` to imports if not present.

- [ ] **Step 3: Build the RateLimit config and pass into Deps**

In `cmd/cronfoundry/serve.go`, find the `webapi.RegisterRoutes(mux, webapi.Deps{...})` block (around line 207). Just before it, add:

```go
trustProxy := envBool(envTrustProxy)
publicBaseURL := os.Getenv(envPublicBaseURL)
if publicBaseURL != "" && !trustProxy {
    slog.Warn("CRONFOUNDRY_PUBLIC_BASE_URL set but CRONFOUNDRY_TRUST_PROXY=false; rate limits will see proxy IP, not client IP")
}
rateCfg := webapi.RateLimiterConfig{
    TrustProxy:       trustProxy,
    Disabled:         envBool(envRateDisabled),
    APIRPM:           envInt(envRateAPIRPM, 60),
    OAuthRPM:         envInt(envRateOAuthRPM, 10),
    WebhookRPM:       envInt(envRateWebhookRPM, 300),
    SSEMaxConcurrent: envInt(envRateSSEConcurrent, 5),
    LRUSize:          envInt(envRateLRUSize, 4096),
}
```

Add `RateLimit: rateCfg,` to the `webapi.Deps{...}` literal alongside the existing fields.

If `publicBaseURL` is already read elsewhere in the file (Task 4 of the CSRF plan added it), reuse the existing variable instead of adding a duplicate read. Verify by grepping `envPublicBaseURL` in the file — there should be exactly one read.

- [ ] **Step 4: Verify**

```bash
go build ./...
go vet ./...
go test -short ./...
```

All clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/cronfoundry/serve.go
git commit -m "ratelimit: read CRONFOUNDRY_RATE_* and TRUST_PROXY env vars"
```

---

### Task 7: Document env vars and Bicep `trustProxy` param

**Files:**
- Modify: `docs/guides/deploy-azure.md`
- Modify: `deploy/params.example.json`
- Modify: `deploy/main.bicep`
- Modify: `deploy/modules/containerApp.bicep`

- [ ] **Step 1: Bicep — add `trustProxy` param**

In `deploy/main.bicep`, near `param publicBaseUrl string = ''` (added in CSRF Task 8), add:

```bicep
@description('Honor the leftmost X-Forwarded-For header for client IP. Set true behind a reverse proxy / Container Apps ingress so per-IP rate limits track real clients, not the proxy IP.')
param trustProxy bool = false
```

Plumb it to the containerApp module call (around the existing `publicBaseUrl: publicBaseUrl` line):

```bicep
publicBaseUrl: publicBaseUrl
trustProxy: trustProxy
```

In `deploy/modules/containerApp.bicep`, add the param near the existing `publicBaseUrl` param:

```bicep
param trustProxy bool = false
```

In the env array for the container, append a new entry near the existing `CRONFOUNDRY_PUBLIC_BASE_URL`:

```bicep
{ name: 'CRONFOUNDRY_TRUST_PROXY', value: string(trustProxy) }
```

- [ ] **Step 2: Update `deploy/params.example.json`**

Add an entry near `publicBaseUrl`:

```json
"trustProxy": { "value": true }
```

(true because the example targets Container Apps with public ingress.)

- [ ] **Step 3: Update `docs/guides/deploy-azure.md`**

Find the parameters table added in CSRF Task 8 (under "Required-in-production parameters worth calling out"). Append rows:

```markdown
| `trustProxy` | Set `true` for any deploy behind a reverse proxy or Container Apps ingress so the leftmost `X-Forwarded-For` is used for rate limiting. Default `false` makes the limiter see the proxy IP and uselessly limit one shared bucket. |
```

Add a short subsection further down explaining the rate-limit env vars are tunable but rarely need touching:

```markdown
### Rate-limit tuning (rarely needed)

The serve container reads these env vars at startup. Defaults match the
release-readiness sizing for a single-operator deploy:

| Env var | Default | What it controls |
|---|---|---|
| `CRONFOUNDRY_RATE_API_RPM` | 60 | Per-IP `/api/*` requests per minute |
| `CRONFOUNDRY_RATE_OAUTH_RPM` | 10 | Per-IP `/oauth/login` + `/oauth/callback` per minute |
| `CRONFOUNDRY_RATE_WEBHOOK_RPM` | 300 | Per-IP `/webhook/github` per minute (sized for GitHub fan-out) |
| `CRONFOUNDRY_RATE_SSE_MAX_CONCURRENT` | 5 | Concurrent live-tail streams per IP |
| `CRONFOUNDRY_RATE_LRU_SIZE` | 4096 | Per-group LRU map size (memory bound) |
| `CRONFOUNDRY_RATE_DISABLED` | false | Kill switch — middleware passes through entirely |

Set any RPM to `0` to disable rate limiting on that group only. These are
operator overrides; they are not exposed as Bicep parameters by default.
Set them via `containerApp.bicep`'s env block if you need persistent values.
```

- [ ] **Step 4: Validate Bicep**

```bash
az bicep build --file deploy/main.bicep --stdout > /dev/null
```

(Skip if `az` not installed locally; CI catches it.)

- [ ] **Step 5: Commit**

```bash
git add deploy/main.bicep deploy/modules/containerApp.bicep deploy/params.example.json docs/guides/deploy-azure.md
git commit -m "ratelimit: document env vars; plumb trustProxy through Bicep"
```

---

### Task 8: Open the PR

- [ ] **Step 1: Push**

```bash
git push -u origin worktree-spec-ratelimit
```

- [ ] **Step 2: Open PR**

```bash
gh pr create --title "feat(security): per-IP rate limiting on /api, /oauth, /webhook + SSE concurrency cap" --body "$(cat <<'EOF'
## Summary

Implements [the rate-limit design](docs/superpowers/specs/2026-04-29-rate-limiting-design.md), closing release-readiness review item #3.

- New `RateLimiter` in `internal/webapi/ratelimit.go` with three per-IP token buckets (`api`, `oauth`, `webhook`) backed by an LRU map per group, plus a per-IP concurrent-stream cap on `/api/runs/{id}/events/stream`
- `clientIP` helper honors `X-Forwarded-For` only when `CRONFOUNDRY_TRUST_PROXY=true`
- Defaults: API 60 rpm (burst 10), OAuth 10/min (burst 3), webhook 300 rpm (burst 50), SSE max 5 concurrent — all tunable via `CRONFOUNDRY_RATE_*` env vars
- 429 responses include `Retry-After` and JSON body
- New direct dep: `github.com/hashicorp/golang-lru/v2`

## Operator note

In production, set `trustProxy: true` (Bicep) → `CRONFOUNDRY_TRUST_PROXY=true`. Without it, every request appears to come from the ingress IP and one shared bucket gets drained for everyone. The server emits a startup `slog.Warn` if `CRONFOUNDRY_PUBLIC_BASE_URL` is set but `TRUST_PROXY` is not.

## Test plan

- [x] `go test ./internal/webapi/...` green (~12 new test cases incl. burst, isolation, LRU eviction, SSE concurrency)
- [x] `go vet ./...` clean
- [x] `az bicep build --file deploy/main.bicep` clean
- [ ] Manual: hit `/oauth/login` 11 times rapidly from one IP; expect 429 with Retry-After
- [ ] Manual: open 6 live-tail SSE streams from one browser; 6th rejects, close one, new one connects
- [ ] Manual: with `CRONFOUNDRY_RATE_DISABLED=true`, hammer `/api/me` — no 429s

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Report PR URL**

---

## Self-review

**Spec coverage:**

- Three route groups + per-IP token bucket → Tasks 3, 5
- SSE concurrency cap → Task 4
- IP source via `clientIP` honoring `TRUST_PROXY` → Task 2
- LRU bound + eviction-gives-fresh-budget semantics → Task 3 test
- 429 with `Retry-After` + JSON body → Task 3 test
- All env-var overrides → Task 6
- Kill switch (`Disabled`) and per-group RPM=0 disable → Task 3 tests
- Bicep plumbing for `trustProxy` → Task 7
- Deploy doc updates → Task 7
- New `golang-lru/v2` dep → Task 1
- Out-of-scope items (distributed flooding, per-user limits, adaptive limits, IP allowlist) — explicitly not implemented; spec lists them as out of scope.

**Placeholder scan:** No "TBD"/"fill in details". One judgment-call note in Task 5 about how to verify a duplicate `envPublicBaseURL` read isn't introduced — that's intentional, the implementer should grep the file once.

**Type consistency:**

- `RateLimiter`, `RateLimiterConfig`, `groupBucket`, `Group`, `SSE`, `clientIP`, `rejectRateLimit`, `NewRateLimiter` — used consistently in Tasks 2-5.
- Field names on `RateLimiterConfig` match between Tasks 3 (definition) and 6 (consumption): `TrustProxy`, `Disabled`, `APIRPM`, `OAuthRPM`, `WebhookRPM`, `SSEMaxConcurrent`, `LRUSize`.
- Env-var names match between spec, Task 6, and Task 7 docs.

**One spec ambiguity caught and resolved inline:** the spec mentioned `slog.Warn` if `ingressExternal` implies trust-proxy needed. The plan uses `CRONFOUNDRY_PUBLIC_BASE_URL` as the proxy proxy-deployment signal instead, since `ingressExternal` is a Bicep-only param the Go binary doesn't see. Task 6 implements this version.
