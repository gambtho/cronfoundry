# Rate Limiting — Technical Design

**Status:** Proposed
**Date:** 2026-04-29
**Author:** gambtho
**Companion:** Release-readiness review (item #3)

## Background

The cronfoundry server exposes three publicly reachable endpoint groups:
`/api/*` (admin UI), `/oauth/*` (login flow), and `/webhook/github` (push
webhook). None today have any rate limiting. PRD nothing explicitly demands
it, but the release-readiness review flagged that for a publicly exposed
single-tenant tool, an unauthenticated `/oauth/login` flood, a probe of
`/webhook/github`, or a single client opening many SSE connections can stall
a small Container App with no defense.

## Threat Model

**In scope:** A single misbehaving client (buggy script, abusive crawler,
attacker probing public endpoints from one IP) hits the server at high rate.
Limits sized for one human operator + one normal browser per IP.

**Out of scope:**

- Distributed flooding (botnets, large-scale credential stuffing) — needs
  a CDN/WAF in front of ingress; in-process limits cannot defend against it.
- Per-user limits (would need session/DB lookups) — YAGNI for MVP.

## Mechanism

Per `(route-group, IP)` we maintain a `*rate.Limiter` (from
`golang.org/x/time/rate`) inside an LRU map (default 4096 entries per group,
oldest evicted on insert). Each request:

1. Resolve client IP (see "IP source" below).
2. Look up or create the limiter for `(group, ip)` with the group's rate +
   burst.
3. `limiter.Allow()` — if false, write `429 Too Many Requests` with
   `Retry-After: 1` and JSON `{"error":"rate limited"}`.

For SSE concurrency, a separate `sync.Map` keyed by IP holds an active-stream
counter. On stream start the middleware atomically increments; if count > cap,
return 429 immediately. On stream close, decrement.

## Route groups & limits

| Group     | Pattern                                | Rate            | Burst | Notes |
|-----------|----------------------------------------|-----------------|-------|---|
| `api`     | `/api/*` (excluding SSE stream)        | 60 / min        | 10    | Covers normal admin UI use + run-now bursts |
| `oauth`   | `/oauth/login`, `/oauth/callback`      | 10 / min        | 3     | Logout exempt — session-bound, harmless |
| `webhook` | `/webhook/github`                      | 300 / min       | 50    | GitHub fan-out on install events legitimately exceeds 60/min |
| `sse`     | `/api/runs/{id}/events/stream`         | concurrency cap | n/a   | Max 5 concurrent streams per IP, no rate budget |

`/internal/*` (runner-only callbacks, JWT-protected) and the SPA static
assets are intentionally **not** rate limited — internal/trusted traffic and
asset fetches respectively.

## IP source

A new env var `CRONFOUNDRY_TRUST_PROXY` (bool, default `false`).

- When `true`: read the leftmost entry of `X-Forwarded-For`; fall back to
  `r.RemoteAddr` if the header is absent or malformed.
- When `false`: always use `r.RemoteAddr`.

Operators behind a reverse proxy (Container Apps ingress, Fly proxy, an
NGINX in front, etc.) MUST set `CRONFOUNDRY_TRUST_PROXY=true` for limits to
be meaningful — otherwise every request appears to come from the proxy IP
and one client can drain the whole bucket.

`cmd/cronfoundry/serve.go` emits a `slog.Warn` at startup if
`CRONFOUNDRY_PUBLIC_BASE_URL` is set (a strong signal the deploy is behind a
proxy) but `CRONFOUNDRY_TRUST_PROXY` is unset/false. Warning only — never
fatal.

## Component sketch

`internal/webapi/ratelimit.go`:

```go
type RateLimiter struct {
    trustProxy    bool
    disabled      bool
    groups        map[string]*groupBucket   // "api" → bucket, etc.
    sseCap        int
    sseConcurrent sync.Map                  // ip → *int64
}

type groupBucket struct {
    limit rate.Limit
    burst int
    mu    sync.Mutex
    lru   *lru.Cache[string, *rate.Limiter] // IP → limiter
}

func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter { ... }

// Group returns a middleware that applies the named group's per-IP limit.
func (rl *RateLimiter) Group(name string, next http.Handler) http.Handler

// SSE returns a middleware that enforces a per-IP concurrent-stream cap.
func (rl *RateLimiter) SSE(next http.Handler) http.Handler
```

`RateLimiterConfig` struct holds: `TrustProxy bool`, `Disabled bool`,
`APIRPM int`, `OAuthRPM int`, `WebhookRPM int`, `SSEMaxConcurrent int`,
`LRUSize int` (default 4096). Bursts are derived: `burst = max(rpm/6, 3)` —
i.e. up to 10s worth of sustained rate. (For api 60 → 10, oauth 10 → 3,
webhook 300 → 50; matches the table above.)

## Wiring

`internal/webapi/server.go` `RegisterRoutes`:

```go
rl := NewRateLimiter(rlCfg)
session := func(h http.Handler) http.Handler {
    return rl.Group("api", RequireSession(deps.MasterKey, h))
}
adminOnly := func(h http.Handler) http.Handler {
    return rl.Group("api", csrfMW(RequireRole(deps.MasterKey, "admin", h)))
}

// OAuth routes
mux.Handle("GET /oauth/login", rl.Group("oauth", http.HandlerFunc(oh.login)))
mux.Handle("GET /oauth/callback", rl.Group("oauth", http.HandlerFunc(oh.callback)))
mux.HandleFunc("GET /oauth/logout", oh.logout) // intentionally not limited

// Webhook
mux.Handle("POST /webhook/github", rl.Group("webhook", wh))

// SSE: concurrency cap composed inside session() so it applies to logged-in
// users; unauthenticated clients bounce off the session check first.
mux.Handle("GET /api/runs/{id}/events/stream",
    session(rl.SSE(http.HandlerFunc(evh.stream))))
```

The order matters:

- For `/api/*`: `rl.Group("api", ...)` is OUTERMOST so an unauthenticated
  flood is bounded before it hits session decoding (cheap CPU win).
- For SSE: rate limit (api group) + session check + SSE concurrency, in
  that order. The api-group limiter still counts the connection-establish
  request; once established, the long-lived SSE doesn't re-poll the bucket.
- For `/oauth/*`: rate limit is the only check (these endpoints precede auth).

## Headers & response

429 response:

```
HTTP/1.1 429 Too Many Requests
Content-Type: application/json
Retry-After: 1

{"error":"rate limited"}
```

For SSE concurrency rejection, `Retry-After: 5` (avoid reconnect storms from
browsers).

`slog.Info` line per rejection: group, IP, path. No request-body content
logged. If volume becomes a problem we can sample at 1-in-10 — observe
first.

## Configuration overrides (operator-tunable)

| Env var                                  | Default | Purpose |
|------------------------------------------|---------|---|
| `CRONFOUNDRY_TRUST_PROXY`                | false   | Honor X-Forwarded-For |
| `CRONFOUNDRY_RATE_API_RPM`               | 60      | API requests per minute per IP |
| `CRONFOUNDRY_RATE_OAUTH_RPM`             | 10      | OAuth requests per minute per IP |
| `CRONFOUNDRY_RATE_WEBHOOK_RPM`           | 300     | Webhook requests per minute per IP |
| `CRONFOUNDRY_RATE_SSE_MAX_CONCURRENT`    | 5       | Concurrent SSE streams per IP |
| `CRONFOUNDRY_RATE_LRU_SIZE`              | 4096    | LRU map size per group (memory bound) |
| `CRONFOUNDRY_RATE_DISABLED`              | false   | Kill switch — middleware passes through |

Setting any RPM to `0` disables that group specifically. `RATE_DISABLED=true`
is a global pass-through.

## Tests

`internal/webapi/ratelimit_test.go`:

| Test | Asserts |
|---|---|
| `TestRateLimiter_AllowsBurst` | `burst` requests allowed; next is 429 |
| `TestRateLimiter_RefillsAfterWindow` | After waiting 1s, refilled by `limit/60` |
| `TestRateLimiter_PerIPIsolation` | IP A 429s; IP B same group still 200 |
| `TestRateLimiter_PerGroupIsolation` | IP A 429s on api but 200 on oauth |
| `TestRateLimiter_TrustProxy_HonorsXFF` | XFF read iff trustProxy=true |
| `TestRateLimiter_NoTrustProxy_IgnoresXFF` | XFF ignored when trustProxy=false |
| `TestRateLimiter_LRU_Eviction` | Fill past capacity; evicted IP gets fresh limiter |
| `TestRateLimiter_SSE_ConcurrencyCap` | 5 streams allowed, 6th 429s, decrement releases |
| `TestRateLimiter_429_Headers` | Retry-After present, body is JSON |
| `TestRateLimiter_Disabled` | All bypass when disabled=true |
| `TestRateLimiter_RPM_Zero_DisablesGroup` | `RATE_API_RPM=0` ⇒ no limit on api |
| `TestRegisterRoutes_ApiRateLimited` | mux integration: 11th request to /api/me 429s |

Tests use `time.Sleep` only in the refill test (1.5s); the rest exercise the
limiter directly with synthesized requests, no real time required.

## Dependencies

- `golang.org/x/time/rate` — already a transitive dep (used by go-git).
- `github.com/hashicorp/golang-lru/v2` — new direct dep. ~50KB, MIT, zero
  external transitive deps. The maintained, well-tested choice. Acceptable
  trade-off vs. a hand-rolled LRU; preference: take the dep.

If a no-new-dep rule is later imposed, swap for a 60-LOC LRU in
`internal/webapi/lru.go` with `container/list` + `map`.

## Operational

- No DB migration.
- No data migration. Counters reset on restart — acceptable for the
  threat model (an attacker has to re-discover us each restart cycle).
- Single-replica deploy today (scheduler can't scale yet per existing
  comments). When multi-replica becomes possible, this design needs a
  shared store (Redis or DB-backed counter) — explicit follow-up, deferred.

## Out of scope

- Distributed flooding (Redis / CDN tier).
- Per-user / per-API-key rate limits.
- Adaptive limits (auto-tighten on detected abuse).
- IP allowlist carve-out for operator IPs.

## Acceptance criteria

1. Each protected endpoint group rejects requests above its limit with
   429 + JSON + `Retry-After`.
2. With `CRONFOUNDRY_TRUST_PROXY=true`, two requests from different
   `X-Forwarded-For` IPs are tracked independently behind the same
   ingress IP.
3. SSE endpoint rejects the 6th concurrent stream from a single IP and
   accepts a new one after a previous closes.
4. `CRONFOUNDRY_RATE_DISABLED=true` makes the middleware a complete
   pass-through (verified by test).
5. The unit-test matrix above passes; `go vet ./...` clean.
6. Deploy guide and `params.example.json` document the new env vars.
