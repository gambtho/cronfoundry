# P2b — GitHub App + Sync Poller Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire CronFoundry up to GitHub: authenticate as a self-hoster's GitHub App, poll each connected repo's default branch, and whenever the HEAD SHA changes, shallow-clone it, parse `cronfoundry.yaml` + every referenced `SKILL.md`, and upsert `skill` + `schedule` rows via sqlc. Expose three operator subcommands — `cronfoundry admin connect-repo`, `list-connections`, `list-schedules` — so the operator can bootstrap and observe the flow headlessly.

**Architecture:** `internal/github/` owns App JWT minting, installation-token caching (50-minute TTL), HEAD-SHA lookup, and authenticated shallow cloning. `internal/sync/` owns the poller loop — per-connection interval, HEAD-first cheap check, conditional clone + parse + DB upsert. Sync ticks are goroutine-per-connection-pool in `cronfoundry serve` (wired up in P2c); for P2b we expose `Poller.SyncOne(ctx, connID)` directly so `cronfoundry admin trigger-sync` (post-P2b) and integration tests can drive single passes. Tests mock the GitHub REST API via `httptest` and exercise the clone path against a local bare repo served over `file://` — no live GitHub required.

**Tech Stack:**
- Go 1.25+ (unchanged)
- `github.com/golang-jwt/jwt/v5` — App JWT signing (new dep)
- `github.com/go-git/go-git/v5` — shallow clone (already in go.mod from P1 writeback)
- `github.com/jackc/pgx/v5`, `internal/db/gen` (sqlc) — DB access
- `sigs.k8s.io/yaml` via `internal/config` — manifest parsing (P1 package, reused)
- Stdlib `net/http`, `crypto/rsa`, `crypto/x509`, `encoding/pem` — token exchange + PEM loading
- `github.com/stretchr/testify`, `github.com/testcontainers/testcontainers-go` — test infra (both existing)

---

## File Structure (locked in upfront)

```
cronfoundry/
├── cmd/cronfoundry/
│   ├── admin.go                              # MODIFY — register the three new subcommands
│   ├── admin_connectrepo.go                  # NEW — `admin connect-repo`
│   ├── admin_connectrepo_test.go             # NEW
│   ├── admin_listconnections.go              # NEW — `admin list-connections`
│   ├── admin_listschedules.go                # NEW — `admin list-schedules`
│   └── admin_listing_test.go                 # NEW — integration test for both listings
├── internal/
│   ├── db/
│   │   ├── queries/
│   │   │   ├── repo_connection.sql           # NEW
│   │   │   ├── skill.sql                     # NEW
│   │   │   └── schedule.sql                  # NEW
│   │   └── gen/                              # REGENERATE via `make sqlc`
│   │       ├── repo_connection.sql.go        # NEW (generated)
│   │       ├── skill.sql.go                  # NEW (generated)
│   │       └── schedule.sql.go               # NEW (generated)
│   ├── github/
│   │   ├── jwt.go                            # NEW — `AppJWT(appID, privateKeyPEM) (string, error)`
│   │   ├── jwt_test.go
│   │   ├── installation.go                   # NEW — `*InstallationCache`, `Token(ctx, installID) (string, error)`
│   │   ├── installation_test.go
│   │   ├── refs.go                           # NEW — `GetBranchHead(ctx, client, baseURL, installToken, owner, name, branch) (string, error)`
│   │   ├── refs_test.go
│   │   ├── clone.go                          # NEW — `CloneAtSHA(ctx, cloneURL, installToken, sha, destDir) error`
│   │   └── clone_test.go
│   └── sync/
│       ├── manifest.go                       # NEW — `LoadManifest(repoRoot) (*config.Manifest, map[string]*config.Skill, error)`
│       ├── manifest_test.go
│       ├── upsert.go                         # NEW — `UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, manifest, skills, sha) error`
│       ├── upsert_test.go
│       ├── poller.go                         # NEW — `Poller.SyncOne(ctx, connID) error`
│       └── poller_test.go
```

### Responsibilities

- `internal/github/jwt.go` — parse PEM private key, mint a signed JWT with `iss=appID`, `iat=now-60s`, `exp=now+9min`. Pure — no network.
- `internal/github/installation.go` — `InstallationCache` holds a map of `installID → (token, expiresAt)` with mutex; `Token()` fetches via `POST /app/installations/{id}/access_tokens` when the cache misses or entry is expired.
- `internal/github/refs.go` — `GET /repos/{owner}/{name}/branches/{branch}` → returns commit SHA.
- `internal/github/clone.go` — go-git's `PlainCloneContext` with `SingleBranch: true`, `Depth: 1`, and `BasicAuth{Username: "x-access-token", Password: installToken}`.
- `internal/sync/manifest.go` — Given a checked-out clone root, reads `cronfoundry.yaml`, validates, then reads and parses each skill's `SKILL.md`.
- `internal/sync/upsert.go` — Pure DB writes: upserts `skill` rows (keyed on `(repo_id, path)`) and `schedule` rows (keyed on `(skill_id, name)`). Schedules absent in the current manifest get `enabled=false`.
- `internal/sync/poller.go` — Orchestrates one connection's sync cycle: HEAD check → if SHA unchanged, touch `last_synced_at` and return; otherwise clone → parse → upsert → mark SHA.

---

## Task 1: Expand sqlc queries for repo_connection, skill, schedule

**Files:**
- Create: `internal/db/queries/repo_connection.sql`
- Create: `internal/db/queries/skill.sql`
- Create: `internal/db/queries/schedule.sql`
- Regenerate: `internal/db/gen/*.go`

- [ ] **Step 1: Write `internal/db/queries/repo_connection.sql`**

```sql
-- name: InsertRepoConnection :one
INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch, sync_interval_sec)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (org_id, owner, name) DO UPDATE
  SET github_app_install_id = EXCLUDED.github_app_install_id,
      default_branch        = EXCLUDED.default_branch,
      sync_interval_sec     = EXCLUDED.sync_interval_sec
RETURNING *;

-- name: GetRepoConnection :one
SELECT *
FROM repo_connection
WHERE id = $1;

-- name: ListRepoConnections :many
SELECT *
FROM repo_connection
WHERE org_id = $1
ORDER BY owner, name;

-- name: ListDueRepoConnections :many
-- Returns repos whose last sync is older than sync_interval_sec, or which
-- have never been synced.
SELECT *
FROM repo_connection
WHERE last_synced_at IS NULL
   OR last_synced_at + make_interval(secs => sync_interval_sec) <= now()
ORDER BY coalesce(last_synced_at, to_timestamp(0));

-- name: MarkRepoSyncedOK :exec
UPDATE repo_connection
SET last_synced_at       = now(),
    last_synced_head_sha = $2,
    last_sync_error      = NULL
WHERE id = $1;

-- name: MarkRepoSyncError :exec
UPDATE repo_connection
SET last_synced_at  = now(),
    last_sync_error = $2
WHERE id = $1;
```

- [ ] **Step 2: Write `internal/db/queries/skill.sql`**

```sql
-- name: UpsertSkill :one
INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (repo_id, path) DO UPDATE
  SET name             = EXCLUDED.name,
      current_sha      = EXCLUDED.current_sha,
      frontmatter_json = EXCLUDED.frontmatter_json,
      updated_at       = now()
RETURNING *;

-- name: ListSkillsByRepo :many
SELECT *
FROM skill
WHERE repo_id = $1
ORDER BY path;

-- name: DeleteMissingSkills :exec
-- Removes skill rows under `repo_id` whose path is NOT in the given slice.
-- Cascades to schedule rows. Called with the list of paths discovered in
-- the current manifest.
DELETE FROM skill
WHERE repo_id = $1
  AND NOT (path = ANY($2::text[]));
```

- [ ] **Step 3: Write `internal/db/queries/schedule.sql`**

```sql
-- name: UpsertSchedule :one
INSERT INTO schedule (
    org_id, skill_id, name, cron, timezone, overlap_policy, timeout_sec,
    enabled, provider, model, llm_secret_ref, llm_endpoint, llm_deployment,
    destinations_json, writeback_json, env_json, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, now())
ON CONFLICT (skill_id, name) DO UPDATE
  SET cron              = EXCLUDED.cron,
      timezone          = EXCLUDED.timezone,
      overlap_policy    = EXCLUDED.overlap_policy,
      timeout_sec       = EXCLUDED.timeout_sec,
      enabled           = EXCLUDED.enabled,
      provider          = EXCLUDED.provider,
      model             = EXCLUDED.model,
      llm_secret_ref    = EXCLUDED.llm_secret_ref,
      llm_endpoint      = EXCLUDED.llm_endpoint,
      llm_deployment    = EXCLUDED.llm_deployment,
      destinations_json = EXCLUDED.destinations_json,
      writeback_json    = EXCLUDED.writeback_json,
      env_json          = EXCLUDED.env_json,
      updated_at        = now()
RETURNING *;

-- name: DisableMissingSchedules :exec
-- Sets enabled=false on any schedule under `skill_id` whose name is NOT in
-- the given slice. Schedules are soft-disabled (not deleted) to preserve
-- run history.
UPDATE schedule
SET enabled    = false,
    updated_at = now()
WHERE skill_id = $1
  AND enabled = true
  AND NOT (name = ANY($2::text[]));

-- name: ListSchedulesByOrg :many
SELECT s.*, sk.path AS skill_path, sk.name AS skill_name, rc.owner, rc.name AS repo_name
FROM schedule s
JOIN skill sk ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE s.org_id = $1
ORDER BY rc.owner, rc.name, sk.path, s.name;
```

- [ ] **Step 4: Regenerate sqlc output**

```bash
make sqlc
```

Expected: `internal/db/gen/repo_connection.sql.go`, `skill.sql.go`, `schedule.sql.go` appear. Existing files (`organization.sql.go`, `secret.sql.go`, `db.go`, `models.go`) are unchanged except `models.go` may grow if sqlc adds more row types.

- [ ] **Step 5: Verify build + vet**

```bash
go build ./...
go vet ./...
```

Expected: both clean.

- [ ] **Step 6: Commit**

```bash
git add internal/db/queries/ internal/db/gen/
git commit -m "feat(db): add sqlc queries for repo_connection, skill, schedule"
```

---

## Task 2: GitHub App JWT minter

**Files:**
- Create: `internal/github/jwt.go`
- Create: `internal/github/jwt_test.go`

- [ ] **Step 1: Add the jwt/v5 dependency**

```bash
go get github.com/golang-jwt/jwt/v5@latest
```

- [ ] **Step 2: Failing test**

```go
// internal/github/jwt_test.go
package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestPEM produces a PKCS#1 RSA key in PEM form suitable for passing
// to AppJWT as the private-key argument.
func generateTestPEM(t *testing.T) (privPEM []byte, pub *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	return privPEM, &priv.PublicKey
}

func TestAppJWT_Signs_And_IncludesExpectedClaims(t *testing.T) {
	privPEM, pub := generateTestPEM(t)

	token, err := AppJWT("12345", privPEM)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	parsed, err := jwtv5.Parse(token, func(t *jwtv5.Token) (interface{}, error) {
		return pub, nil
	}, jwtv5.WithValidMethods([]string{"RS256"}))
	require.NoError(t, err)
	require.True(t, parsed.Valid)

	claims := parsed.Claims.(jwtv5.MapClaims)
	assert.Equal(t, "12345", claims["iss"])

	iat, ok := claims["iat"].(float64)
	require.True(t, ok)
	exp, ok := claims["exp"].(float64)
	require.True(t, ok)
	// exp should be 9 minutes beyond iat (GitHub cap is 10 minutes; we leave
	// a 1-minute cushion).
	assert.InDelta(t, 9*60, exp-iat, 5)

	// iat should be slightly in the past (clock-skew tolerance).
	now := float64(time.Now().Unix())
	assert.Less(t, iat, now+1)
	assert.Greater(t, iat, now-120)
}

func TestAppJWT_RejectsMalformedPEM(t *testing.T) {
	_, err := AppJWT("12345", []byte("not a pem"))
	require.Error(t, err)
}

func TestAppJWT_RejectsNonRSAKey(t *testing.T) {
	// A valid PEM block that isn't an RSA key.
	bad := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")})
	_, err := AppJWT("12345", bad)
	require.Error(t, err)
}
```

- [ ] **Step 3: Run the test, confirm it fails**

```bash
go test ./internal/github/... -run TestAppJWT -v
```

Expected: `AppJWT` undefined.

- [ ] **Step 4: Implement `internal/github/jwt.go`**

```go
// Package github wraps the GitHub App authentication + REST API calls
// CronFoundry needs for repo sync and writeback. Higher-level orchestration
// (the sync loop, dispatcher) lives in internal/sync and internal/scheduler.
package github

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// AppJWT mints a short-lived (9-minute) JWT signed with the GitHub App's
// private key. Use this token as the Authorization bearer when calling
// GitHub App-level endpoints — specifically to exchange for per-installation
// access tokens.
//
// The returned JWT includes:
//   - iss = appID (numeric App identifier, passed as a string)
//   - iat = now - 60s (clock-skew tolerance)
//   - exp = now + 9min (GitHub rejects >10min; we leave a 1min cushion)
func AppJWT(appID string, privateKeyPEM []byte) (string, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return "", fmt.Errorf("github: appJWT: parse PEM: no PEM block")
	}
	var priv *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		p, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("github: appJWT: parse PKCS1: %w", err)
		}
		priv = p
	case "PRIVATE KEY":
		keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("github: appJWT: parse PKCS8: %w", err)
		}
		p, ok := keyAny.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("github: appJWT: PKCS8 key is not RSA")
		}
		priv = p
	default:
		return "", fmt.Errorf("github: appJWT: unexpected PEM type %q", block.Type)
	}

	now := time.Now()
	claims := jwtv5.MapClaims{
		"iss": appID,
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
	}
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodRS256, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		return "", fmt.Errorf("github: appJWT: sign: %w", err)
	}
	return signed, nil
}
```

- [ ] **Step 5: Run the test, confirm it passes**

```bash
go test ./internal/github/... -run TestAppJWT -v
```

Expected: all three sub-tests PASS.

- [ ] **Step 6: `go vet ./...` clean**

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/github/jwt.go internal/github/jwt_test.go
git commit -m "feat(github): mint GitHub App JWTs for auth exchange"
```

---

## Task 3: Installation-token fetcher + cache

**Files:**
- Create: `internal/github/installation.go`
- Create: `internal/github/installation_test.go`

- [ ] **Step 1: Failing test**

```go
// internal/github/installation_test.go
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tokenServer struct {
	*httptest.Server
	calls *int32
}

func newTokenServer(t *testing.T, token string, expiresAt time.Time) *tokenServer {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/app/installations/")
		assert.Contains(t, r.URL.Path, "/access_tokens")
		auth := r.Header.Get("Authorization")
		assert.True(t, len(auth) > 7 && auth[:7] == "Bearer ", "want Bearer <JWT>, got %q", auth)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      token,
			"expires_at": expiresAt.UTC().Format(time.RFC3339),
		})
	}))
	return &tokenServer{Server: srv, calls: &calls}
}

func TestInstallationCache_FetchesAndCaches(t *testing.T) {
	expires := time.Now().Add(55 * time.Minute)
	srv := newTokenServer(t, "ghs_abc123", expires)
	defer srv.Close()

	privPEM, _ := generateTestPEM(t)
	cache := NewInstallationCache(InstallationCacheConfig{
		AppID:        "42",
		PrivateKey:   privPEM,
		BaseURL:      srv.URL,
		HTTPClient:   srv.Client(),
		Clock:        func() time.Time { return time.Now() },
		TokenTTL:     50 * time.Minute,
	})

	tok, err := cache.Token(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, "ghs_abc123", tok)

	// Second call within TTL: no additional HTTP request.
	tok2, err := cache.Token(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, "ghs_abc123", tok2)
	assert.Equal(t, int32(1), atomic.LoadInt32(srv.calls))
}

func TestInstallationCache_RefetchesOnExpiry(t *testing.T) {
	expires := time.Now().Add(1 * time.Hour)
	srv := newTokenServer(t, "ghs_first", expires)
	defer srv.Close()

	privPEM, _ := generateTestPEM(t)

	// Controllable clock.
	var virtualNow atomic.Pointer[time.Time]
	t0 := time.Now()
	virtualNow.Store(&t0)
	clock := func() time.Time { return *virtualNow.Load() }

	cache := NewInstallationCache(InstallationCacheConfig{
		AppID:        "42",
		PrivateKey:   privPEM,
		BaseURL:      srv.URL,
		HTTPClient:   srv.Client(),
		Clock:        clock,
		TokenTTL:     50 * time.Minute,
	})

	_, err := cache.Token(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(srv.calls))

	// Advance clock past TTL; next call should refetch.
	future := t0.Add(time.Hour)
	virtualNow.Store(&future)
	_, err = cache.Token(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(srv.calls))
}

func TestInstallationCache_PropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"bad creds"}`)
	}))
	defer srv.Close()

	privPEM, _ := generateTestPEM(t)
	cache := NewInstallationCache(InstallationCacheConfig{
		AppID:        "42",
		PrivateKey:   privPEM,
		BaseURL:      srv.URL,
		HTTPClient:   srv.Client(),
		Clock:        time.Now,
		TokenTTL:     50 * time.Minute,
	})

	_, err := cache.Token(context.Background(), 99)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}
```

- [ ] **Step 2: Run test, expect undefined errors**

```bash
go test ./internal/github/... -run TestInstallationCache -v
```

- [ ] **Step 3: Implement `internal/github/installation.go`**

```go
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// InstallationCacheConfig configures a cache. Only AppID and PrivateKey are
// required; the rest have sensible defaults for production use.
type InstallationCacheConfig struct {
	AppID      string
	PrivateKey []byte          // PEM bytes
	BaseURL    string          // default: "https://api.github.com"
	HTTPClient *http.Client    // default: http.DefaultClient
	Clock      func() time.Time // default: time.Now
	TokenTTL   time.Duration    // default: 50 * time.Minute
}

// InstallationCache holds per-installation access tokens and mints new ones
// via the GitHub App JWT when cached tokens expire.
//
// Thread-safe; one cache per process.
type InstallationCache struct {
	cfg     InstallationCacheConfig
	mu      sync.Mutex
	entries map[int64]cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

func NewInstallationCache(cfg InstallationCacheConfig) *InstallationCache {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.github.com"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = 50 * time.Minute
	}
	return &InstallationCache{cfg: cfg, entries: map[int64]cachedToken{}}
}

// Token returns an installation access token for installID, minting a fresh
// one via the GitHub App JWT exchange when the cache entry is missing or
// expired.
func (c *InstallationCache) Token(ctx context.Context, installID int64) (string, error) {
	c.mu.Lock()
	entry, ok := c.entries[installID]
	now := c.cfg.Clock()
	c.mu.Unlock()

	if ok && now.Before(entry.expiresAt) {
		return entry.token, nil
	}

	tok, expires, err := c.fetch(ctx, installID)
	if err != nil {
		return "", err
	}

	// Use the shorter of (server's expires_at, TokenTTL from now).
	ttlExpiry := now.Add(c.cfg.TokenTTL)
	if expires.Before(ttlExpiry) {
		ttlExpiry = expires
	}

	c.mu.Lock()
	c.entries[installID] = cachedToken{token: tok, expiresAt: ttlExpiry}
	c.mu.Unlock()
	return tok, nil
}

// fetch calls POST /app/installations/{id}/access_tokens and parses the
// response.
func (c *InstallationCache) fetch(ctx context.Context, installID int64) (string, time.Time, error) {
	jwt, err := AppJWT(c.cfg.AppID, c.cfg.PrivateKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: mint JWT: %w", err)
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.cfg.BaseURL, installID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", time.Time{}, fmt.Errorf("github: installation: http %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: decode: %w", err)
	}
	expires, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("github: installation: parse expires_at: %w", err)
	}
	return payload.Token, expires, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/github/... -run TestInstallationCache -v
```

Expected: all three tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/github/installation.go internal/github/installation_test.go
git commit -m "feat(github): add installation-token cache with TTL-based refresh"
```

---

## Task 4: Branch HEAD-SHA lookup

**Files:**
- Create: `internal/github/refs.go`
- Create: `internal/github/refs_test.go`

- [ ] **Step 1: Failing test**

```go
// internal/github/refs_test.go
package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBranchHead_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/myorg/myrepo/branches/main", r.URL.Path)
		assert.Equal(t, "token ghs_abc", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":   "main",
			"commit": map[string]any{"sha": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
		})
	}))
	defer srv.Close()

	sha, err := GetBranchHead(context.Background(), srv.Client(), srv.URL, "ghs_abc", "myorg", "myrepo", "main")
	require.NoError(t, err)
	assert.Equal(t, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", sha)
}

func TestGetBranchHead_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := GetBranchHead(context.Background(), srv.Client(), srv.URL, "ghs_abc", "o", "r", "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}
```

- [ ] **Step 2: Run, expect undefined.**

- [ ] **Step 3: Implement `internal/github/refs.go`**

```go
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetBranchHead returns the commit SHA at the tip of the named branch.
// installToken is the short-lived token minted via InstallationCache.
func GetBranchHead(
	ctx context.Context,
	client *http.Client,
	baseURL, installToken, owner, name, branch string,
) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/branches/%s", baseURL, owner, name, branch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("github: GetBranchHead: new request: %w", err)
	}
	req.Header.Set("Authorization", "token "+installToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: GetBranchHead: http do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("github: GetBranchHead: http %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("github: GetBranchHead: decode: %w", err)
	}
	if payload.Commit.SHA == "" {
		return "", fmt.Errorf("github: GetBranchHead: empty sha in response")
	}
	return payload.Commit.SHA, nil
}
```

- [ ] **Step 4: Run tests, expect PASS. `go vet ./...` clean. Commit.**

```bash
git add internal/github/refs.go internal/github/refs_test.go
git commit -m "feat(github): GetBranchHead lookup via REST"
```

---

## Task 5: Clone-at-SHA via go-git

**Files:**
- Create: `internal/github/clone.go`
- Create: `internal/github/clone_test.go`

- [ ] **Step 1: Failing test — uses a local bare repo on disk via `file://`**

```go
// internal/github/clone_test.go
package github

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeBareFixtureRepo creates a bare repository on disk with a single commit
// on `main` and returns (cloneURL, commitSHA).
func makeBareFixtureRepo(t *testing.T) (cloneURL, sha string) {
	t.Helper()
	root := t.TempDir()

	// Worktree repo first, then push its HEAD into a bare mirror.
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "bare.git")

	wRepo, err := git.PlainInit(work, false)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(work, "hello.txt"), []byte("hi\n"), 0o644))

	wt, err := wRepo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("hello.txt")
	require.NoError(t, err)
	hash, err := wt.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@e", When: time.Now()},
	})
	require.NoError(t, err)

	// Rename the default branch to `main` to match real-world GitHub repos.
	// go-git uses `master` by default; we relabel.
	// Easiest path: create `main` from HEAD then keep master too (harmless).
	head, err := wRepo.Head()
	require.NoError(t, err)
	// Create `main` at the same commit.
	mainRef := plumbingReference("refs/heads/main", head.Hash())
	require.NoError(t, wRepo.Storer.SetReference(mainRef))

	// Clone into a bare repo to serve as the remote.
	_, err = git.PlainClone(bare, true, &git.CloneOptions{URL: work})
	require.NoError(t, err)

	return "file://" + bare, hash.String()
}

// Small shim so the test file doesn't pull the plumbing package directly in
// its import block.
func plumbingReference(name string, hash plumbingHash) plumbingRef { return plumbingRef{name: name, hash: hash} }

func TestCloneAtSHA_Success(t *testing.T) {
	url, sha := makeBareFixtureRepo(t)
	dest := t.TempDir()

	err := CloneAtSHA(context.Background(), url, "" /* no auth for file:// */, sha, dest)
	require.NoError(t, err)

	// Verify hello.txt materialised at the expected commit.
	contents, err := os.ReadFile(filepath.Join(dest, "hello.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hi\n", string(contents))
}

func TestCloneAtSHA_UnreachableURL(t *testing.T) {
	err := CloneAtSHA(context.Background(), "file:///nonexistent.git", "", "deadbeef", t.TempDir())
	require.Error(t, err)
}
```

The test above uses two tiny type-alias shims (`plumbingReference`, `plumbingRef`, `plumbingHash`) to avoid importing go-git plumbing types in the test's top-level block. Add the aliases as part of the real implementation file or a separate test-helper file. Simpler: import directly in the test. Here's a cleaner version **replacing the above with direct imports**:

Replace `makeBareFixtureRepo` in `clone_test.go` with this version (no shims):

```go
import (
	"github.com/go-git/go-git/v5/plumbing"
)

func makeBareFixtureRepo(t *testing.T) (cloneURL, sha string) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "bare.git")

	wRepo, err := git.PlainInit(work, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(work, "hello.txt"), []byte("hi\n"), 0o644))

	wt, err := wRepo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add("hello.txt")
	require.NoError(t, err)
	hash, err := wt.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@e", When: time.Now()},
	})
	require.NoError(t, err)

	// Point refs/heads/main at HEAD so PlainClone can find it via SingleBranch.
	mainRef := plumbing.NewHashReference(plumbing.ReferenceName("refs/heads/main"), hash)
	require.NoError(t, wRepo.Storer.SetReference(mainRef))

	_, err = git.PlainClone(bare, true, &git.CloneOptions{URL: work})
	require.NoError(t, err)

	return "file://" + bare, hash.String()
}
```

And delete the `plumbingReference` / `plumbingRef` / `plumbingHash` helpers — they're unnecessary with the direct import.

- [ ] **Step 2: Run test, expect undefined `CloneAtSHA`.**

- [ ] **Step 3: Implement `internal/github/clone.go`**

```go
package github

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// CloneAtSHA performs a shallow (depth=1) single-branch clone and checks out
// the specific commit SHA. installToken is the GitHub installation access
// token, used as basic-auth password. For file:// URLs (tests), pass "".
//
// destDir must be an empty (or nonexistent) directory. The function creates
// a fresh clone; it does not mutate an existing checkout.
func CloneAtSHA(ctx context.Context, cloneURL, installToken, sha, destDir string) error {
	opts := &git.CloneOptions{
		URL:          cloneURL,
		Depth:        1,
		SingleBranch: true,
	}
	if installToken != "" {
		opts.Auth = &githttp.BasicAuth{
			Username: "x-access-token",
			Password: installToken,
		}
	}
	repo, err := git.PlainCloneContext(ctx, destDir, false, opts)
	if err != nil {
		return fmt.Errorf("github: CloneAtSHA: clone %s: %w", cloneURL, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("github: CloneAtSHA: worktree: %w", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(sha)}); err != nil {
		return fmt.Errorf("github: CloneAtSHA: checkout %s: %w", sha, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests + vet. Commit.**

```bash
git add internal/github/clone.go internal/github/clone_test.go
git commit -m "feat(github): shallow-clone a repo at a specific SHA via go-git"
```

---

## Task 6: Sync manifest reader

**Files:**
- Create: `internal/sync/manifest.go`
- Create: `internal/sync/manifest_test.go`

- [ ] **Step 1: Failing test**

```go
// internal/sync/manifest_test.go
package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func TestLoadManifest_ReadsYAMLAndSkills(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cronfoundry.yaml", `version: 1
skills:
  - path: skills/digest
    schedules:
      - name: monday
        cron: "0 9 * * MON"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`)
	writeFile(t, root, "skills/digest/SKILL.md", `---
name: weekly-digest
description: Tiny digest
---
Hello.
`)

	m, skills, err := LoadManifest(root)
	require.NoError(t, err)
	require.NotNil(t, m)
	require.Len(t, m.Skills, 1)

	skill, ok := skills["skills/digest"]
	require.True(t, ok, "skill must be keyed by its path")
	assert.Equal(t, "weekly-digest", skill.Frontmatter.Name)
}

func TestLoadManifest_MissingYAML(t *testing.T) {
	root := t.TempDir()
	_, _, err := LoadManifest(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cronfoundry.yaml")
}

func TestLoadManifest_MissingSKILL(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cronfoundry.yaml", `version: 1
skills:
  - path: skills/missing
    schedules:
      - { name: x, cron: "* * * * *", provider: openai, model: m, destinations: [] }
`)
	_, _, err := LoadManifest(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SKILL.md")
}
```

- [ ] **Step 2: Run, expect undefined.**

- [ ] **Step 3: Implement `internal/sync/manifest.go`**

```go
// Package sync implements the repo sync poller (Loop 1 in the P2 design):
// periodically HEAD-check each connected repo, shallow-clone on SHA change,
// parse cronfoundry.yaml + referenced SKILL.md files, upsert skill +
// schedule rows.
package sync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gambtho/cronfoundry/internal/config"
)

// LoadManifest reads and validates a checked-out repo's cronfoundry.yaml,
// then parses each SKILL.md it references. Returns (manifest, skillsByPath)
// or an error on any step.
func LoadManifest(repoRoot string) (*config.Manifest, map[string]*config.Skill, error) {
	manifestPath := filepath.Join(repoRoot, "cronfoundry.yaml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("sync: read cronfoundry.yaml: %w", err)
	}
	m, err := config.ParseManifest(manifestBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("sync: parse cronfoundry.yaml: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, nil, fmt.Errorf("sync: validate cronfoundry.yaml: %w", err)
	}

	skills := make(map[string]*config.Skill, len(m.Skills))
	for _, entry := range m.Skills {
		skillMD := filepath.Join(repoRoot, entry.Path, "SKILL.md")
		data, err := os.ReadFile(skillMD)
		if err != nil {
			return nil, nil, fmt.Errorf("sync: read SKILL.md for %q: %w", entry.Path, err)
		}
		sk, err := config.ParseSkillFile(data)
		if err != nil {
			return nil, nil, fmt.Errorf("sync: parse SKILL.md for %q: %w", entry.Path, err)
		}
		skills[entry.Path] = sk
	}
	return m, skills, nil
}
```

- [ ] **Step 4: Tests pass. Commit.**

```bash
git add internal/sync/manifest.go internal/sync/manifest_test.go
git commit -m "feat(sync): read cronfoundry.yaml and referenced SKILL.md files"
```

---

## Task 7: Upsert skills + schedules

**Files:**
- Create: `internal/sync/upsert.go`
- Create: `internal/sync/upsert_test.go`

- [ ] **Step 1: Failing integration test using testcontainers-postgres**

```go
// internal/sync/upsert_test.go
package sync

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gambtho/cronfoundry/internal/config"
	"github.com/gambtho/cronfoundry/internal/db"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// Local variant of the shared testcontainers helper. (P2d will extract a
// shared internal/testdb package; for now each integration test boots its
// own container.)
func startPG(t *testing.T) (*pgxpool.Pool, pgtype.UUID, pgtype.UUID, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("cf_test"),
		postgres.WithUsername("cf"),
		postgres.WithPassword("cf"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, dsn))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('test-org') RETURNING id`).Scan(&orgID))

	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))

	return pool, orgID, repoID, func() { pool.Close(); _ = c.Terminate(context.Background()) }
}

func TestUpsertSkillsAndSchedules_FullRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	manifestYAML := `version: 1
skills:
  - path: skills/a
    schedules:
      - name: hourly
        cron: "0 * * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`
	m, err := config.ParseManifest([]byte(manifestYAML))
	require.NoError(t, err)
	require.NoError(t, m.Validate())

	skills := map[string]*config.Skill{
		"skills/a": {
			Frontmatter: config.SkillFrontmatter{Name: "a"},
			Body:        "prompt",
		},
	}

	ctx := context.Background()
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, m, skills, "sha-initial"))

	// Assert: one skill, one schedule, enabled.
	q := dbgen.New(pool)
	listed, err := q.ListSkillsByRepo(ctx, repoID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "skills/a", listed[0].Path)
	assert.Equal(t, "sha-initial", listed[0].CurrentSha)

	var name string
	var enabled bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT name, enabled FROM schedule WHERE skill_id = $1`, listed[0].ID).Scan(&name, &enabled))
	assert.Equal(t, "hourly", name)
	assert.True(t, enabled)

	// Second sync: drop the `hourly` schedule; add `daily`.
	manifestYAML2 := `version: 1
skills:
  - path: skills/a
    schedules:
      - name: daily
        cron: "0 9 * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`
	m2, _ := config.ParseManifest([]byte(manifestYAML2))
	require.NoError(t, m2.Validate())
	require.NoError(t, UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, m2, skills, "sha-second"))

	var hourlyEnabled bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT enabled FROM schedule WHERE skill_id = $1 AND name = 'hourly'`, listed[0].ID).Scan(&hourlyEnabled))
	assert.False(t, hourlyEnabled, "removed schedule should be soft-disabled, not deleted")

	var dailyEnabled bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT enabled FROM schedule WHERE skill_id = $1 AND name = 'daily'`, listed[0].ID).Scan(&dailyEnabled))
	assert.True(t, dailyEnabled)

	_ = json.Marshal // quiet unused-import lints in case json isn't used below
}
```

- [ ] **Step 2: Run test, expect undefined.**

- [ ] **Step 3: Implement `internal/sync/upsert.go`**

```go
package sync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gambtho/cronfoundry/internal/config"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// UpsertSkillsAndSchedules reconciles the parsed manifest + skill frontmatters
// against the DB:
//   - skills present in the manifest are upserted (keyed on repo_id+path)
//   - skills absent from the manifest are deleted (cascades to their schedules)
//   - for each manifest skill, schedules absent from the manifest are soft-
//     disabled (enabled=false) to preserve run history; schedules present are
//     upserted and marked enabled.
//
// All DB work happens in a single transaction.
func UpsertSkillsAndSchedules(
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, repoID pgtype.UUID,
	manifest *config.Manifest,
	skills map[string]*config.Skill,
	sha string,
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sync: upsert: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := dbgen.New(tx)

	// 1) Upsert skills in the manifest.
	presentPaths := make([]string, 0, len(manifest.Skills))
	skillByPath := make(map[string]pgtype.UUID, len(manifest.Skills))
	for _, entry := range manifest.Skills {
		sk, ok := skills[entry.Path]
		if !ok {
			return fmt.Errorf("sync: upsert: missing SKILL.md for %q", entry.Path)
		}
		fmBytes, err := json.Marshal(sk.Frontmatter)
		if err != nil {
			return fmt.Errorf("sync: upsert: marshal frontmatter for %q: %w", entry.Path, err)
		}
		row, err := q.UpsertSkill(ctx, dbgen.UpsertSkillParams{
			OrgID:           orgID,
			RepoID:          repoID,
			Path:            entry.Path,
			Name:            sk.Frontmatter.Name,
			CurrentSha:      sha,
			FrontmatterJson: fmBytes,
		})
		if err != nil {
			return fmt.Errorf("sync: upsert skill %q: %w", entry.Path, err)
		}
		presentPaths = append(presentPaths, entry.Path)
		skillByPath[entry.Path] = row.ID
	}

	// 2) Delete skill rows no longer present (cascades to schedules).
	if err := q.DeleteMissingSkills(ctx, dbgen.DeleteMissingSkillsParams{
		RepoID:  repoID,
		Column2: presentPaths,
	}); err != nil {
		return fmt.Errorf("sync: delete missing skills: %w", err)
	}

	// 3) Per-skill: upsert schedules, disable missing.
	for _, entry := range manifest.Skills {
		skillID := skillByPath[entry.Path]

		presentNames := make([]string, 0, len(entry.Schedules))
		for _, sch := range entry.Schedules {
			destBytes, err := json.Marshal(sch.Destinations)
			if err != nil {
				return fmt.Errorf("sync: marshal destinations for %q/%q: %w", entry.Path, sch.Name, err)
			}
			var writebackBytes []byte
			if sch.Writeback != nil {
				writebackBytes, err = json.Marshal(sch.Writeback)
				if err != nil {
					return fmt.Errorf("sync: marshal writeback for %q/%q: %w", entry.Path, sch.Name, err)
				}
			}
			envBytes, err := json.Marshal(sch.Env)
			if err != nil {
				return fmt.Errorf("sync: marshal env for %q/%q: %w", entry.Path, sch.Name, err)
			}

			// Apply Schedule defaults via EffectiveOverlapPolicy (see P2a T3).
			overlap := sch.EffectiveOverlapPolicy()
			timeoutSec := sch.TimeoutSec
			if timeoutSec == 0 {
				timeoutSec = 600
			}
			timezone := sch.Timezone
			if timezone == "" {
				timezone = "UTC"
			}

			_, err = q.UpsertSchedule(ctx, dbgen.UpsertScheduleParams{
				OrgID:            orgID,
				SkillID:          skillID,
				Name:             sch.Name,
				Cron:             sch.Cron,
				Timezone:         timezone,
				OverlapPolicy:    overlap,
				TimeoutSec:       int32(timeoutSec),
				Enabled:          true,
				Provider:         sch.Provider,
				Model:            sch.Model,
				LlmSecretRef:     pgtype.Text{}, // optional; not set here
				LlmEndpoint:      pgtype.Text{},
				LlmDeployment:    pgtype.Text{},
				DestinationsJson: destBytes,
				WritebackJson:    writebackBytes,
				EnvJson:          envBytes,
			})
			if err != nil {
				return fmt.Errorf("sync: upsert schedule %q/%q: %w", entry.Path, sch.Name, err)
			}
			presentNames = append(presentNames, sch.Name)
		}

		if err := q.DisableMissingSchedules(ctx, dbgen.DisableMissingSchedulesParams{
			SkillID: skillID,
			Column2: presentNames,
		}); err != nil {
			return fmt.Errorf("sync: disable missing schedules for %q: %w", entry.Path, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sync: upsert: commit: %w", err)
	}
	return nil
}
```

**SDK-drift note:** the sqlc-generated `UpsertSkillParams`, `DeleteMissingSkillsParams`, `UpsertScheduleParams`, `DisableMissingSchedulesParams` field names follow sqlc's conventions. The `Column2` suffix reflects sqlc's default naming for positional `$2::text[]` parameters — verify with `grep -E "type (UpsertSkill|DeleteMissingSkills|UpsertSchedule|DisableMissingSchedules)Params struct" internal/db/gen/*.go` and adjust the call sites. If the field is named `Paths` or `Names` instead, update accordingly.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/sync/... -run TestUpsert -v
```

Expected: the full round-trip test PASSES, demonstrating upsert → re-sync soft-disable.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/upsert.go internal/sync/upsert_test.go
git commit -m "feat(sync): upsert skills and schedules in a single transaction"
```

---

## Task 8: Poller orchestration (SyncOne)

**Files:**
- Create: `internal/sync/poller.go`
- Create: `internal/sync/poller_test.go`

- [ ] **Step 1: Failing test — end-to-end via file:// clone URL + mocked installation-token endpoint**

```go
// internal/sync/poller_test.go
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/github"
)

// buildRepo creates a bare repo seeded with a cronfoundry.yaml + SKILL.md
// and returns (cloneURL, headSHA).
func buildRepo(t *testing.T, manifestYAML, skillMD string) (string, string) {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	bare := filepath.Join(root, "bare.git")

	r, err := git.PlainInit(work, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(work, "cronfoundry.yaml"), []byte(manifestYAML), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(work, "skills", "a"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, "skills", "a", "SKILL.md"), []byte(skillMD), 0o644))

	wt, err := r.Worktree()
	require.NoError(t, err)
	_, err = wt.Add(".")
	require.NoError(t, err)
	sha, err := wt.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@e", When: time.Now()},
	})
	require.NoError(t, err)
	require.NoError(t, r.Storer.SetReference(plumbing.NewHashReference(
		plumbing.ReferenceName("refs/heads/main"), sha,
	)))
	_, err = git.PlainClone(bare, true, &git.CloneOptions{URL: work})
	require.NoError(t, err)
	return "file://" + bare, sha.String()
}

// fakeGHServer stubs the branch-head endpoint so the poller can HEAD-check.
func fakeGHServer(t *testing.T, sha string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/repos/")
		assert.Contains(t, r.URL.Path, "/branches/main")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"commit": map[string]any{"sha": sha},
		})
	}))
}

// fakeTokenServer stubs the installation-token exchange.
func fakeTokenServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/app/installations/")
		assert.Contains(t, r.URL.Path, "/access_tokens")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_test",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
}

func TestPoller_SyncOne_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	manifestYAML := `version: 1
skills:
  - path: skills/a
    schedules:
      - name: hourly
        cron: "0 * * * *"
        provider: openai
        model: gpt-4o-mini
        destinations:
          - slack: { secret: slack_webhook }
`
	skillMD := `---
name: a
description: tiny
---
prompt
`

	cloneURL, sha := buildRepo(t, manifestYAML, skillMD)

	pool, orgID, repoID, cleanup := startPG(t)
	defer cleanup()

	// Overwrite the clone URL on the repo_connection so SyncOne uses the
	// file:// URL we just built. (In production this URL is minted from the
	// GitHub installation token; see `poller.go::cloneURLFor`. Tests inject
	// via a per-poller override.)
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`UPDATE repo_connection SET owner='o', name='r', default_branch='main' WHERE id = $1`,
		repoID)
	require.NoError(t, err)

	ghSrv := fakeGHServer(t, sha)
	defer ghSrv.Close()
	tokSrv := fakeTokenServer(t)
	defer tokSrv.Close()

	privPEM, _ := github.MustTestPrivateKey(t) // helper lives in poller_test.go; see below

	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID:      "42",
		PrivateKey: privPEM,
		BaseURL:    tokSrv.URL,
		HTTPClient: tokSrv.Client(),
		Clock:      time.Now,
		TokenTTL:   50 * time.Minute,
	})

	p := NewPoller(PollerConfig{
		Pool:          pool,
		OrgID:         orgID,
		Installations: cache,
		GitHubBaseURL: ghSrv.URL,
		HTTPClient:    ghSrv.Client(),
		CloneURLFor: func(owner, name string) string {
			// In tests, ignore owner/name and route straight to the file:// fixture.
			return cloneURL
		},
	})

	require.NoError(t, p.SyncOne(ctx, repoID))

	// Assert: skill + schedule inserted.
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM skill WHERE repo_id = $1`, repoID).Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM schedule`).Scan(&count))
	assert.Equal(t, 1, count)

	// last_synced_head_sha should be set.
	var gotSHA *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_synced_head_sha FROM repo_connection WHERE id = $1`, repoID).Scan(&gotSHA))
	require.NotNil(t, gotSHA)
	assert.Equal(t, sha, *gotSHA)

	// Second call: SHA unchanged, no re-clone, but last_synced_at advances.
	var firstSynced time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_synced_at FROM repo_connection WHERE id = $1`, repoID).Scan(&firstSynced))
	time.Sleep(20 * time.Millisecond) // ensure timestamp advances
	require.NoError(t, p.SyncOne(ctx, repoID))
	var secondSynced time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_synced_at FROM repo_connection WHERE id = $1`, repoID).Scan(&secondSynced))
	assert.True(t, secondSynced.After(firstSynced))

	// Still one skill, one schedule (no duplicates from idempotent upsert).
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM skill WHERE repo_id = $1`, repoID).Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM schedule`).Scan(&count))
	assert.Equal(t, 1, count)

	_ = fmt.Sprint // silence unused-import lint warnings for fmt
}
```

**Helper — add to `internal/github/testing.go` (NEW, exported for cross-package test use):**

```go
// internal/github/testing.go
// Package-level test helpers. Contents here are exported so tests in other
// packages (e.g. internal/sync) can reuse PEM generation without duplicating.
package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

// MustTestPrivateKey returns a newly-generated 2048-bit RSA key in PKCS#1 PEM
// form. Intended for tests only; the exported capital-M name lets other
// packages' test files call it.
func MustTestPrivateKey(t *testing.T) ([]byte, *rsa.PublicKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("github: MustTestPrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	return pemBytes, &priv.PublicKey
}
```

- [ ] **Step 2: Run test, expect `NewPoller`, `PollerConfig`, `SyncOne` undefined.**

- [ ] **Step 3: Implement `internal/sync/poller.go`**

```go
package sync

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/github"
)

// PollerConfig bundles the poller's collaborators.
type PollerConfig struct {
	Pool          *pgxpool.Pool
	OrgID         pgtype.UUID
	Installations *github.InstallationCache
	GitHubBaseURL string                         // "https://api.github.com" in prod
	HTTPClient    *http.Client                   // default: http.DefaultClient
	CloneURLFor   func(owner, name string) string // default: x-access-token HTTPS
}

// Poller orchestrates repo-sync passes. P2c will add a Run() loop that
// schedules SyncOne against all due repo_connection rows on a ticker.
type Poller struct {
	cfg PollerConfig
}

// NewPoller constructs a Poller with sensible defaults.
func NewPoller(cfg PollerConfig) *Poller {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.GitHubBaseURL == "" {
		cfg.GitHubBaseURL = "https://api.github.com"
	}
	if cfg.CloneURLFor == nil {
		cfg.CloneURLFor = func(owner, name string) string {
			return fmt.Sprintf("https://github.com/%s/%s.git", owner, name)
		}
	}
	return &Poller{cfg: cfg}
}

// SyncOne runs a single sync pass for the named repo_connection.
//
// Flow:
//   1. Load the repo_connection row.
//   2. Fetch an installation token.
//   3. HEAD-check the default branch via the GitHub API.
//   4. If the SHA matches last_synced_head_sha, touch last_synced_at and return.
//   5. Otherwise shallow-clone the repo at the new SHA into a tempdir.
//   6. Parse cronfoundry.yaml + SKILL.md files.
//   7. Upsert skill + schedule rows.
//   8. Mark the repo_connection as synced at the new SHA.
//
// Errors at any step are recorded in last_sync_error and surfaced to the
// caller; the row's last_synced_at is still advanced so a broken repo
// doesn't back up the queue.
func (p *Poller) SyncOne(ctx context.Context, connID pgtype.UUID) error {
	q := dbgen.New(p.cfg.Pool)

	row, err := q.GetRepoConnection(ctx, connID)
	if err != nil {
		return fmt.Errorf("sync: SyncOne: load conn: %w", err)
	}

	token, err := p.cfg.Installations.Token(ctx, row.GithubAppInstallID)
	if err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("install token: %w", err))
	}

	headSHA, err := github.GetBranchHead(
		ctx, p.cfg.HTTPClient, p.cfg.GitHubBaseURL, token,
		row.Owner, row.Name, row.DefaultBranch,
	)
	if err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("head check: %w", err))
	}

	// Cheap path: SHA unchanged.
	if row.LastSyncedHeadSha != nil && *row.LastSyncedHeadSha == headSHA {
		if err := q.MarkRepoSyncedOK(ctx, dbgen.MarkRepoSyncedOKParams{
			ID:                 connID,
			LastSyncedHeadSha:  pgtype.Text{String: headSHA, Valid: true},
		}); err != nil {
			return fmt.Errorf("sync: mark unchanged: %w", err)
		}
		return nil
	}

	// Expensive path: clone, parse, upsert.
	tmp, err := os.MkdirTemp("", "cronfoundry-clone-")
	if err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("tempdir: %w", err))
	}
	defer os.RemoveAll(tmp)

	cloneURL := p.cfg.CloneURLFor(row.Owner, row.Name)
	if err := github.CloneAtSHA(ctx, cloneURL, token, headSHA, tmp); err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("clone: %w", err))
	}

	manifest, skills, err := LoadManifest(tmp)
	if err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("load manifest: %w", err))
	}

	if err := UpsertSkillsAndSchedules(ctx, p.cfg.Pool, p.cfg.OrgID, connID, manifest, skills, headSHA); err != nil {
		return p.markErr(ctx, q, connID, fmt.Errorf("upsert: %w", err))
	}

	if err := q.MarkRepoSyncedOK(ctx, dbgen.MarkRepoSyncedOKParams{
		ID:                connID,
		LastSyncedHeadSha: pgtype.Text{String: headSHA, Valid: true},
	}); err != nil {
		return fmt.Errorf("sync: mark synced ok: %w", err)
	}
	return nil
}

// markErr persists the error to last_sync_error and returns it wrapped.
// last_synced_at is still advanced so we retry on the normal cadence rather
// than tight-looping on a broken repo.
func (p *Poller) markErr(ctx context.Context, q *dbgen.Queries, connID pgtype.UUID, err error) error {
	msg := err.Error()
	_ = q.MarkRepoSyncError(ctx, dbgen.MarkRepoSyncErrorParams{
		ID:            connID,
		LastSyncError: pgtype.Text{String: msg, Valid: true},
	})
	return fmt.Errorf("sync: SyncOne: %w", err)
}
```

**SDK-drift checks:**
- `GetRepoConnection` may return a struct whose SHA field is named `LastSyncedHeadSha *string` (pointer) or `LastSyncedHeadSha pgtype.Text` (struct). The test reads it as `*string`; the sqlc config in P2a (`emit_pointers_for_null_types: true`) produced pointer fields, so `*string` is correct. Verify with `grep "LastSyncedHeadSha" internal/db/gen/repo_connection.sql.go`.
- `MarkRepoSyncedOKParams` may name the SHA field `LastSyncedHeadSha` or `Column2`. Verify.
- If a sqlc-generated field is differently-named, adjust call sites — the load-bearing contract is the behavior the test asserts.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/sync/... -v
```

Expected: `TestPoller_SyncOne_EndToEnd` and `TestUpsertSkillsAndSchedules_FullRoundTrip` both PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/poller.go internal/sync/poller_test.go internal/github/testing.go
git commit -m "feat(sync): Poller.SyncOne — end-to-end repo sync"
```

---

## Task 9: `cronfoundry admin connect-repo`

**Files:**
- Create: `cmd/cronfoundry/admin_connectrepo.go`
- Create: `cmd/cronfoundry/admin_connectrepo_test.go`
- Modify: `cmd/cronfoundry/admin.go` to register the new subcommand

- [ ] **Step 1: Register the subcommand — `cmd/cronfoundry/admin.go`**

```go
package main

import (
	"github.com/spf13/cobra"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Operator utilities — init, secrets, repos, triggers",
	}
	cmd.AddCommand(newAdminInitCmd())
	cmd.AddCommand(newAdminSetSecretCmd())
	cmd.AddCommand(newAdminConnectRepoCmd())
	return cmd
}
```

- [ ] **Step 2: Implement `cmd/cronfoundry/admin_connectrepo.go`**

```go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminConnectRepoCmd() *cobra.Command {
	var installationID int64
	var defaultBranch string
	var syncInterval int

	cmd := &cobra.Command{
		Use:   "connect-repo <owner/name>",
		Short: "Add (or update) a GitHub repo connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminConnectRepo(cmd.Context(), args[0], installationID, defaultBranch, syncInterval, os.Stdout)
		},
	}
	cmd.Flags().Int64Var(&installationID, "installation-id", 0, "GitHub App installation ID (required)")
	cmd.Flags().StringVar(&defaultBranch, "branch", "main", "default branch to poll")
	cmd.Flags().IntVar(&syncInterval, "sync-interval-sec", 60, "seconds between sync polls")
	_ = cmd.MarkFlagRequired("installation-id")
	return cmd
}

func runAdminConnectRepo(ctx context.Context, repo string, installID int64, branch string, syncSec int, out *os.File) error {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return fmt.Errorf("repo must be owner/name; got %q", repo)
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	if err != nil {
		return fmt.Errorf("no organization seeded; run `cronfoundry admin init` first: %w", err)
	}

	row, err := q.InsertRepoConnection(ctx, dbgen.InsertRepoConnectionParams{
		OrgID:              org.ID,
		GithubAppInstallID: installID,
		Owner:              owner,
		Name:               name,
		DefaultBranch:      branch,
		SyncIntervalSec:    int32(syncSec),
	})
	if err != nil {
		return fmt.Errorf("insert repo connection: %w", err)
	}

	fmt.Fprintf(out, "Connected %s/%s (install=%d, branch=%s, interval=%ds)\n",
		row.Owner, row.Name, row.GithubAppInstallID, row.DefaultBranch, row.SyncIntervalSec)
	return nil
}
```

- [ ] **Step 3: Write tests — `cmd/cronfoundry/admin_connectrepo_test.go`**

```go
package main

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminConnectRepo_MissingDatabaseURL(t *testing.T) {
	t.Setenv(envDatabaseURL, "")
	err := runAdminConnectRepo(context.Background(), "o/r", 1, "main", 60, os.Stdout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envDatabaseURL)
}

func TestAdminConnectRepo_BadRepoFormat(t *testing.T) {
	t.Setenv(envDatabaseURL, "postgres://example")
	err := runAdminConnectRepo(context.Background(), "not-a-slash", 1, "main", 60, os.Stdout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owner/name")
}

func TestAdminConnectRepo_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()

	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "test-org"))

	err := runAdminConnectRepo(context.Background(), "myorg/myrepo", 42, "main", 60, os.Stdout)
	require.NoError(t, err)

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	defer pool.Close()
	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM repo_connection WHERE owner='myorg' AND name='myrepo'`).Scan(&count))
	assert.Equal(t, 1, count)
}
```

- [ ] **Step 4: Run tests, expect PASS**

```bash
go test ./cmd/cronfoundry/... -run TestAdminConnectRepo -v
```

- [ ] **Step 5: Commit**

```bash
git add cmd/cronfoundry/admin.go cmd/cronfoundry/admin_connectrepo.go cmd/cronfoundry/admin_connectrepo_test.go
git commit -m "feat(admin): cronfoundry admin connect-repo"
```

---

## Task 10: `admin list-connections` + `admin list-schedules`

**Files:**
- Create: `cmd/cronfoundry/admin_listconnections.go`
- Create: `cmd/cronfoundry/admin_listschedules.go`
- Create: `cmd/cronfoundry/admin_listing_test.go`
- Modify: `cmd/cronfoundry/admin.go` (register two new subcommands)

- [ ] **Step 1: Register subcommands — `cmd/cronfoundry/admin.go`**

Replace the existing `newAdminCmd` body so that AFTER Task 9's addition, the file reads:

```go
package main

import "github.com/spf13/cobra"

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Operator utilities — init, secrets, repos, triggers",
	}
	cmd.AddCommand(newAdminInitCmd())
	cmd.AddCommand(newAdminSetSecretCmd())
	cmd.AddCommand(newAdminConnectRepoCmd())
	cmd.AddCommand(newAdminListConnectionsCmd())
	cmd.AddCommand(newAdminListSchedulesCmd())
	return cmd
}
```

- [ ] **Step 2: Implement `cmd/cronfoundry/admin_listconnections.go`**

```go
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminListConnectionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-connections",
		Short: "List connected GitHub repos and their last sync state",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminListConnections(cmd.Context(), os.Stdout)
		},
	}
}

func runAdminListConnections(ctx context.Context, out io.Writer) error {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	if err != nil {
		return fmt.Errorf("no organization: %w", err)
	}
	rows, err := q.ListRepoConnections(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintln(out, "(no connected repos)")
		return nil
	}
	tw := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tBRANCH\tINSTALL\tINTERVAL\tLAST SYNC\tSTATE")
	for _, r := range rows {
		sync := "never"
		if r.LastSyncedAt != nil {
			sync = r.LastSyncedAt.Format("2006-01-02 15:04:05")
		}
		state := "ok"
		if r.LastSyncError != nil && *r.LastSyncError != "" {
			state = "ERROR"
		}
		fmt.Fprintf(tw, "%s/%s\t%s\t%d\t%ds\t%s\t%s\n",
			r.Owner, r.Name, r.DefaultBranch, r.GithubAppInstallID, r.SyncIntervalSec, sync, state)
	}
	return tw.Flush()
}
```

- [ ] **Step 3: Implement `cmd/cronfoundry/admin_listschedules.go`**

```go
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminListSchedulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-schedules",
		Short: "List all schedules discovered from connected repos",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminListSchedules(cmd.Context(), os.Stdout)
		},
	}
}

func runAdminListSchedules(ctx context.Context, out io.Writer) error {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	if err != nil {
		return fmt.Errorf("no organization: %w", err)
	}
	rows, err := q.ListSchedulesByOrg(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintln(out, "(no schedules discovered yet)")
		return nil
	}
	tw := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tSKILL\tSCHEDULE\tCRON\tPROVIDER\tENABLED")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s/%s\t%s\t%s\t%s\t%s\t%t\n",
			r.Owner, r.RepoName, r.SkillPath, r.Name, r.Cron, r.Provider, r.Enabled)
	}
	return tw.Flush()
}
```

- [ ] **Step 4: Write tests — `cmd/cronfoundry/admin_listing_test.go`**

```go
package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminListConnections_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	var buf bytes.Buffer
	require.NoError(t, runAdminListConnections(context.Background(), &buf))
	assert.Contains(t, buf.String(), "no connected repos")
}

func TestAdminListSchedules_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	var buf bytes.Buffer
	require.NoError(t, runAdminListSchedules(context.Background(), &buf))
	assert.Contains(t, buf.String(), "no schedules")
}

func TestAdminListConnections_ShowsRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))
	require.NoError(t, runAdminConnectRepo(context.Background(), "foo/bar", 7, "main", 60, nil))

	var buf bytes.Buffer
	require.NoError(t, runAdminListConnections(context.Background(), &buf))
	assert.Contains(t, buf.String(), "foo/bar")
	assert.Contains(t, buf.String(), "never")
}
```

**Important:** the `runAdminConnectRepo` signature in Task 9 passes `out *os.File` and would fail `nil`. Verify the signature — if it does `Fprintf(out, ...)`, passing `nil` panics. Fix by changing `out *os.File` → `out io.Writer` in Task 9's signature, and fix the test here accordingly. (If you're executing the tasks in order, do the refactor at this point in Task 10 — it's a one-line change.)

- [ ] **Step 5: Refactor `runAdminConnectRepo` signature to `io.Writer`**

```go
// cmd/cronfoundry/admin_connectrepo.go — change:
func runAdminConnectRepo(ctx context.Context, repo string, installID int64, branch string, syncSec int, out io.Writer) error { ... }
```

Update the `RunE` callsite to still pass `os.Stdout`; `*os.File` satisfies `io.Writer`. This allows the `TestAdminListConnections_ShowsRow` test to pass `nil` (or a `bytes.Buffer`) without panicking.

Actually, `bytes.Buffer` is safer than `nil` — update the test to pass `&bytes.Buffer{}` instead of `nil` if nil panics on `fmt.Fprintf`.

- [ ] **Step 6: Run tests**

```bash
go test ./cmd/cronfoundry/... -v
```

Expected: all tests from T9 + the 3 new listing tests PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/cronfoundry/admin.go cmd/cronfoundry/admin_listconnections.go cmd/cronfoundry/admin_listschedules.go cmd/cronfoundry/admin_listing_test.go cmd/cronfoundry/admin_connectrepo.go
git commit -m "feat(admin): list-connections and list-schedules subcommands"
```

---

## Task 11: `admin trigger-sync` + end-to-end test

**Files:**
- Create: `cmd/cronfoundry/admin_triggersync.go`
- Create: `cmd/cronfoundry/admin_triggersync_test.go`
- Modify: `cmd/cronfoundry/admin.go` to register `trigger-sync`

- [ ] **Step 1: Register in `cmd/cronfoundry/admin.go`**

```go
package main

import "github.com/spf13/cobra"

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Operator utilities — init, secrets, repos, triggers",
	}
	cmd.AddCommand(newAdminInitCmd())
	cmd.AddCommand(newAdminSetSecretCmd())
	cmd.AddCommand(newAdminConnectRepoCmd())
	cmd.AddCommand(newAdminListConnectionsCmd())
	cmd.AddCommand(newAdminListSchedulesCmd())
	cmd.AddCommand(newAdminTriggerSyncCmd())
	return cmd
}
```

- [ ] **Step 2: Implement `cmd/cronfoundry/admin_triggersync.go`**

```go
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/sync"
)

const (
	envGitHubAppID  = "CRONFOUNDRY_GITHUB_APP_ID"
	envGitHubAppPEM = "CRONFOUNDRY_GITHUB_APP_PEM"
)

func newAdminTriggerSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trigger-sync <owner/name>",
		Short: "Force an immediate sync pass on a connected repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminTriggerSync(cmd.Context(), args[0], os.Stdout)
		},
	}
}

func runAdminTriggerSync(ctx context.Context, repo string, out io.Writer) error {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	appID := os.Getenv(envGitHubAppID)
	if appID == "" {
		return fmt.Errorf("%s is required", envGitHubAppID)
	}
	pemPath := os.Getenv(envGitHubAppPEM)
	if pemPath == "" {
		return fmt.Errorf("%s is required (path to GitHub App private key PEM)", envGitHubAppPEM)
	}
	pemBytes, err := os.ReadFile(pemPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", envGitHubAppPEM, err)
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok {
		return fmt.Errorf("repo must be owner/name; got %q", repo)
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	if err != nil {
		return fmt.Errorf("no organization: %w", err)
	}

	// Find the connection by owner/name.
	rows, err := q.ListRepoConnections(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("list connections: %w", err)
	}
	var connID = pgtypeUUIDZero()
	for _, r := range rows {
		if r.Owner == owner && r.Name == name {
			connID = r.ID
			break
		}
	}
	if !connID.Valid {
		return fmt.Errorf("no connection for %s/%s; run `cronfoundry admin connect-repo` first", owner, name)
	}

	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID:      appID,
		PrivateKey: pemBytes,
	})
	poller := sync.NewPoller(sync.PollerConfig{
		Pool:          pool,
		OrgID:         org.ID,
		Installations: cache,
	})

	if err := poller.SyncOne(ctx, connID); err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	fmt.Fprintf(out, "Synced %s/%s\n", owner, name)
	return nil
}

// pgtypeUUIDZero returns a UUID with Valid=false for "not found" sentinel use.
func pgtypeUUIDZero() pgtypeUUID { return pgtypeUUID{} }

// Local alias to avoid importing pgtype in this file's interface surface.
type pgtypeUUID = struct {
	Bytes [16]byte
	Valid bool
}
```

Wait — pgtype.UUID is a struct with a different shape. Replace the local alias + `pgtypeUUIDZero` with direct `pgtype.UUID` usage. The fix:

```go
import "github.com/jackc/pgx/v5/pgtype"

// ...
var connID pgtype.UUID
for _, r := range rows {
	if r.Owner == owner && r.Name == name {
		connID = r.ID
		break
	}
}
if !connID.Valid {
	return fmt.Errorf("no connection for %s/%s; run `cronfoundry admin connect-repo` first", owner, name)
}
```

And delete the `pgtypeUUIDZero` / `pgtypeUUID` aliases. The default-initialized `pgtype.UUID{}` has `Valid = false`.

- [ ] **Step 3: Integration test — `cmd/cronfoundry/admin_triggersync_test.go`**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/github"
)

func TestAdminTriggerSync_MissingEnv(t *testing.T) {
	t.Setenv(envDatabaseURL, "")
	t.Setenv(envGitHubAppID, "")
	t.Setenv(envGitHubAppPEM, "")
	err := runAdminTriggerSync(context.Background(), "o/r", &bytes.Buffer{})
	require.Error(t, err)
}

// TestAdminTriggerSync_EndToEnd exercises the full flow: init, connect,
// sync against a file:// fixture repo + mocked token endpoint.
//
// This test does NOT use the production CloneURLFor + InstallationCache.
// The sync path inside `runAdminTriggerSync` builds these from env vars;
// to inject fakes we'd need more wiring (DI). Skipping this as a full E2E —
// internal/sync/poller_test.go already covers the SyncOne E2E path.
// We keep this integration test lightweight: verify the happy-path plumbing
// produces useful error messages.
func TestAdminTriggerSync_MissingConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	t.Setenv(envGitHubAppID, "42")

	// Write a throwaway PEM to a temp file.
	priv, _ := github.MustTestPrivateKey(t)
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(pemPath, priv, 0o600))
	t.Setenv(envGitHubAppPEM, pemPath)

	require.NoError(t, runAdminInit(context.Background(), "o"))

	err := runAdminTriggerSync(context.Background(), "none/here", &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no connection")
}

// unused imports guard against over-aggressive `goimports` stripping.
var (
	_ = git.PlainInit
	_ = plumbing.NewHashReference
	_ = object.Signature{}
	_ = json.Marshal
	_ = http.MethodGet
	_ = httptest.NewServer
	_ = time.Second
)
```

(The final `var (...)` block is defensive; delete unused import aliases if the test doesn't reference them. Goimports can be fussy.)

- [ ] **Step 4: Run tests**

```bash
go test ./cmd/cronfoundry/... -v
```

Expected: all tests pass including T9 + T10 + T11.

- [ ] **Step 5: Full suite**

```bash
go test -short ./...
go vet ./...
```

- [ ] **Step 6: Commit**

```bash
git add cmd/cronfoundry/admin.go cmd/cronfoundry/admin_triggersync.go cmd/cronfoundry/admin_triggersync_test.go
git commit -m "feat(admin): cronfoundry admin trigger-sync — manual sync pass"
```

---

## Self-Review

**1. Spec coverage (P2 design § Loop 1 + Loop 1-adjacent operator tools)**

| P2 spec requirement | Plan task |
|---|---|
| GitHub App JWT minting | T2 |
| Installation-token cache + refresh | T3 |
| HEAD-SHA lookup | T4 |
| Shallow clone at SHA via install token | T5 |
| Parse cronfoundry.yaml + SKILL.md from checked-out clone | T6 |
| Upsert skill + schedule rows (soft-disable missing) | T7 |
| Poller.SyncOne orchestration | T8 |
| `cronfoundry admin connect-repo` | T9 |
| `cronfoundry admin list-connections` + `list-schedules` | T10 |
| Manual sync trigger for testing | T11 |

Not in this plan (intentionally, per P2 decomposition):
- `Poller.Run()` loop (ticker-driven, will live inside `cronfoundry serve` — P2c)
- GitHub webhooks — deferred to P2.5 or later
- Scheduler (`next_fire_at` tick loop) — P2c
- Runner HTTP mode + `/internal` API — P2c

**2. Placeholder scan**

Searched for TBD/TODO/fill-in/handle edge cases — only legitimate occurrences in the "SDK-drift note" sections which explicitly tell the implementer to verify + adjust. All code blocks are complete.

**3. Type consistency**

- `*InstallationCache`, `InstallationCacheConfig`, `Token(ctx, id int64)` consistent between T3 definition and T8 usage.
- `GetBranchHead(ctx, client, baseURL, token, owner, name, branch)` signature consistent between T4 definition and T8 usage.
- `CloneAtSHA(ctx, url, token, sha, dest)` signature consistent between T5 and T8.
- `LoadManifest(repoRoot) (*config.Manifest, map[string]*config.Skill, error)` consistent between T6 and T8.
- `UpsertSkillsAndSchedules(ctx, pool, orgID, repoID, manifest, skills, sha)` consistent between T7 and T8.
- `PollerConfig`, `NewPoller`, `SyncOne(ctx, connID)` consistent between T8 definition and T11 usage.
- `runAdminConnectRepo`'s `out` parameter: Task 9 originally had `out *os.File`; Task 10's tests pass a `bytes.Buffer`, so Task 10 steps 5 flags the refactor to `io.Writer`. **This is a plan defect; it should be `io.Writer` from Task 9.** — **Fixed inline**: update Task 9 Step 2 code snippet below to use `io.Writer` from the start.

**4. Known weak points**

- sqlc's generated parameter field names for positional-only args (like `$2::text[]`) default to `Column2` — may differ by sqlc version. T7's SDK-drift note addresses this.
- `MustTestPrivateKey` is exported from `internal/github/testing.go` so cross-package tests can reuse it. File name `testing.go` is arguably `_test.go`-worthy, but `_test.go` files can't be imported by non-test packages. Current naming + `_ "testing"` import is the conventional pattern.
- End-to-end test in T11 is intentionally light — a full E2E exists in T8. T11's integration test verifies CLI wiring (env vars → DB lookup → poller construction), not the sync mechanics themselves.

---

**Inline fix to Task 9 Step 2:** change the signature in `admin_connectrepo.go` to use `io.Writer` instead of `*os.File`, import `io`, and update the `RunE` closure to pass `os.Stdout` (still satisfies `io.Writer`):

```go
// cmd/cronfoundry/admin_connectrepo.go
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminConnectRepoCmd() *cobra.Command {
	var installationID int64
	var defaultBranch string
	var syncInterval int

	cmd := &cobra.Command{
		Use:   "connect-repo <owner/name>",
		Short: "Add (or update) a GitHub repo connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminConnectRepo(cmd.Context(), args[0], installationID, defaultBranch, syncInterval, os.Stdout)
		},
	}
	cmd.Flags().Int64Var(&installationID, "installation-id", 0, "GitHub App installation ID (required)")
	cmd.Flags().StringVar(&defaultBranch, "branch", "main", "default branch to poll")
	cmd.Flags().IntVar(&syncInterval, "sync-interval-sec", 60, "seconds between sync polls")
	_ = cmd.MarkFlagRequired("installation-id")
	return cmd
}

func runAdminConnectRepo(ctx context.Context, repo string, installID int64, branch string, syncSec int, out io.Writer) error {
	// ... body unchanged ...
}
```

This removes the inconsistency that would have required the Task 10 refactor step. The `os` import is retained because it's still used for `os.Stdout` and `os.Getenv`.

---

Plan complete and saved to `docs/superpowers/plans/2026-04-20-p2b-github-sync.md`.
