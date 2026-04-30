# CSRF Protection — Technical Design

**Status:** Proposed
**Date:** 2026-04-29
**Author:** gambtho
**Companion:** Release-readiness review (item #2)

## Background

PRD `2026-04-19-cronfoundry-prd.md` NFR-2.4 requires "CSRF protection on all
mutating endpoints." A grep of the codebase finds no CSRF implementation —
only the OAuth `state` parameter, which protects the login flow but not
post-login mutations.

The session cookie `cf_session` is `HttpOnly; Secure; SameSite=Lax`. SameSite
Lax already blocks the most common cross-site CSRF vectors (cross-origin
fetches, top-level POST navigations), so this work is primarily
defense-in-depth and explicit spec compliance, not closing a gaping hole.

## Threat Model

**In scope:** A logged-in admin browses a malicious page with their
`cf_session` live. The attacker tries to invoke a state-changing CronFoundry
API to spend LLM budget, exfiltrate or rotate secrets, or create users.

**Vectors covered by this design:**

- Cross-site fetch / form POST with cookies (SameSite=Lax already blocks; we
  add explicit token + Origin checks as belt-and-suspenders)
- GET-with-side-effect mistakes (none today, but adding any in the future
  doesn't immediately become exploitable because Origin still checks)
- Subdomain takeover scenarios where an attacker controls a sibling domain
  and can set cookies on the parent — Origin/Referer match catches this
- Browser bugs or older browsers that mis-handle SameSite

**Out of scope:**

- `POST /webhook/github` — HMAC-verified, no session, machine-to-machine
- `GET /internal/runs/{id}/copilot-token` — runner-side, JWT-protected
- `/oauth/login`, `/oauth/callback`, `/oauth/logout` — already use signed
  `state` for CSRF on the OAuth dance itself

## Mechanism: Double-Submit Cookie + Origin Check

A second cookie `cf_csrf` carries a per-session opaque random token that the
SPA reads and echoes in an `X-CSRF-Token` header on every mutating request.
The server middleware compares the cookie value to the header value with a
constant-time compare. Because an attacker on a foreign origin cannot read
cookies set on the cronfoundry origin, they cannot forge a matching header.

In addition, the middleware verifies the `Origin` header (with `Referer` host
fallback) against an operator-configured allowlist. This catches confused-
deputy bugs and any class of attack where the cookie and header are both
predictable but the request originates off-origin.

### Cookie issuance

Issued in the OAuth callback handler immediately after `cf_session`:

```ini
Name:     cf_csrf
Value:    base64url(crypto/rand 32 bytes)
HttpOnly: false        ← so the SPA can read via document.cookie
SameSite: Lax          ← matches session cookie
Secure:   !isLocalhost
MaxAge:   same as cf_session (currently 7 days idle)
Path:     /
```

The token is opaque random bytes — not derived from the session, not signed,
not stored server-side. Security primitive is *cookie presence on the
cronfoundry origin*; an attacker on a different origin cannot read or set it.

`/oauth/logout` clears `cf_csrf` alongside `cf_session`.

### Server middleware

`internal/webapi/csrf.go`:

```go
func CSRF(cfg CSRFConfig) func(http.Handler) http.Handler
// where CSRFConfig{ AllowedOrigin string }
```

Logic:

1. If method is `GET`, `HEAD`, or `OPTIONS` → pass through.
2. Else (POST, PATCH, PUT, DELETE):
   1. Read `cf_csrf` cookie. Missing → `403 csrf cookie missing`.
   2. Read `X-CSRF-Token` header. Missing → `403 csrf header missing`.
   3. `subtle.ConstantTimeCompare(cookie, header)`. Mismatch → `403 csrf mismatch`.
   4. Origin check:
      - If `allowedOrigin` is empty (dev mode), skip step 4.
      - Else read `Origin` header. If absent, fall back to scheme+host of
        `Referer`. If neither is present, → `403 origin missing`.
      - Compare scheme+host (case-insensitive) to `allowedOrigin`. Mismatch
        → `403 origin mismatch`.

All 403 responses log a structured `slog` line at INFO level: path, method,
which check failed. Token values are never logged. The 403 body is JSON:
`{"error":"<reason>"}`.

### Wiring

`internal/webapi/server.go` wraps `RequireRole` from outside with the CSRF
middleware so CSRF runs *before* auth — an unauthenticated mutating
request gets a CSRF 403 (or origin 403) without paying session decoding
costs:

```go
csrfMW := CSRF(CSRFConfig{AllowedOrigin: deps.PublicBaseURL})
adminOnly := func(h http.Handler) http.Handler {
    return csrfMW(RequireRole(deps.MasterKey, "admin", h))
}
```

Routes already using `adminOnly` (POST/PATCH/DELETE for repos, schedules,
secrets, users, copilot-connect) are covered automatically. `POST /webhook/github`
is wired with the raw webhook handler, not `adminOnly`, and remains
out-of-scope. The session-only mutating routes — there are currently none in
`server.go`; all GETs use `session()`, all mutations use `adminOnly()` — would
need explicit `CSRF()` wrapping if added.

### Configuration

New env var: `CRONFOUNDRY_PUBLIC_BASE_URL` (e.g. `https://cronfoundry.example.com`).

- Read once at server start; passed into `CSRF()` middleware via `CSRFConfig`.
- If unset and `CRONFOUNDRY_ENV != production` (or equivalent dev signal),
  Origin check is disabled. In production we fail to start if unset, with
  an actionable error.
- Documented in `docs/guides/deploy-azure.md` and `deploy/params.example.json`.

### Front-end

`web/src/lib/api.ts` — `apiFetch` reads the cookie and sets the header:

```ts
function csrfToken(): string | undefined {
  return document.cookie.split('; ')
    .find(c => c.startsWith('cf_csrf='))?.split('=')[1];
}

const headers = new Headers(init?.headers);
const method = (init?.method ?? 'GET').toUpperCase();
if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
  const t = csrfToken();
  if (t) headers.set('X-CSRF-Token', t);
}
```

Single touch point — every existing `api.*` caller works unchanged. `apiFetch`
already redirects to `/oauth/login` on 401; extend to also redirect on
403 responses whose body matches `{"error":"csrf <...>"}` so a stale-cookie
case re-auths gracefully.

### First-load handling

After OAuth callback → SPA redirect, both cookies are set before the SPA's
first POST. No race. If a tab loses the `cf_csrf` cookie (manual clear,
expiry skew), the next mutation gets `403 csrf cookie missing`, the SPA
redirects to login, the user re-auths, and both cookies are re-issued.

## Tests

`internal/webapi/csrf_test.go` (new):

| Case | Expected |
|---|---|
| GET, no token | 200 (passthrough) |
| HEAD, no token | 200 |
| POST, no cookie, no header | 403 cookie-missing |
| POST, cookie present, no header | 403 header-missing |
| POST, cookie ≠ header | 403 mismatch |
| POST, cookie = header, allowedOrigin empty, no Origin | 200 (dev) |
| POST, cookie = header, Origin matches | 200 |
| POST, cookie = header, Origin mismatches | 403 origin-mismatch |
| POST, cookie = header, no Origin, Referer matches | 200 |
| POST, cookie = header, no Origin, no Referer, prod mode | 403 origin-missing |
| Token compare uses ConstantTimeCompare (no early-exit timing) | unit test asserting equal-length compare |

`internal/webapi/oauth_test.go` (extend):

- Callback sets `cf_csrf` with HttpOnly=false, SameSite=Lax, MaxAge matches
  session, Secure flag matches host.
- Logout clears `cf_csrf` (sets MaxAge<0).
- `cf_csrf` value is 43 chars (base64url of 32 bytes, unpadded) and
  cryptographically random across two callbacks.

Existing handler tests (`secrets_test.go`, `users_test.go`, `repos_test.go`,
`schedules_test.go`, `copilot_connect_test.go`): a single shared helper
`withCSRF(req)` injects matching cookie + header. Each test that issues a
mutating request is updated to call `withCSRF(req)` once. No per-test logic
duplicated.

`web/src/lib/api.test.ts` (new or extension): mock `document.cookie`,
verify mutating fetches set `X-CSRF-Token`, verify GETs do not.

## Operational

- No DB migration.
- No data migration. Existing live sessions don't have `cf_csrf` — first
  mutating request post-deploy 403s, the SPA redirects to login, user
  re-auths. Documented in release notes.
- Rollout: enforced by default; no flag. The plan considered a
  `CRONFOUNDRY_CSRF_ENFORCE` toggle for a transitional release cycle but
  shipped enforced — for a self-hosted single-tenant tool with a handful
  of admins, the SPA's 403→/oauth/login redirect is a sufficient re-auth
  path with no operator-side toggle needed.
- Audit log: existing audit middleware logs admin mutations. CSRF rejection
  happens before the audit middleware (it's chained inside `adminOnly` but
  before the role-checked handler runs), so failed CSRF attempts produce
  slog INFO lines but no audit row. That's intentional — audit is for
  authenticated, role-passed actions only.

## Out of scope (explicit non-goals)

- Per-request token rotation. Per-session is sufficient and concurrency-safe.
- Server-stored token (synchronizer pattern). Double-submit is sufficient
  given the same-origin SPA model.
- CSRF protection on `/webhook/github` — the threat model there is an
  unauthenticated attacker, not a logged-in user with a hijacked session;
  HMAC verification covers it.
- Replacing OAuth `state`. Out of scope; this is independent.

## Acceptance criteria

1. Every existing `adminOnly` route rejects requests without matching
   cf_csrf cookie + X-CSRF-Token header with a 403.
2. `CRONFOUNDRY_PUBLIC_BASE_URL` set in prod rejects requests with
   mismatched Origin/Referer with a 403.
3. The React UI continues to work end-to-end (manual smoke + the existing
   integration tests pass).
4. New unit tests in `internal/webapi/csrf_test.go` cover the matrix above.
5. The deploy runbook and `params.example.json` document the new env var.
6. PRD NFR-2.4 is satisfied; reference this doc from the PRD.
