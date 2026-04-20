# P2c — Scheduler + API + Runner HTTP Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the P2 loop. Add the scheduler tick, per-run JWT minting, `/internal` HTTP API, subprocess dispatcher with lifecycle supervision, and the runner's HTTP-client mode so `cronfoundry runner --run-id <id>` fetches its context from the API, streams events back, and finalizes. Wire all three loops (sync + scheduler + API) into `cronfoundry serve`. After P2c lands, a self-hoster can run `cronfoundry serve`, let cron boundaries pass, and watch scheduled skill invocations fire end-to-end against real LLMs + destinations.

**Architecture:** `internal/token/` mints + verifies HS256 JWTs derived via HKDF from the master key. `internal/cloud/` defines a `JobDispatcher` interface with a `SubprocessDispatcher` implementation (the only P2 impl; P4 adds `ContainerAppsJobDispatcher`). `internal/scheduler/` runs the 30s tick loop, inserts `run` rows idempotently, applies overlap policies, and dispatches. `internal/api/` is a thin `http.ServeMux` with handlers for run context, secrets (scoped by JWT claim), clone-URL, events, finalize, and the admin-triggered manual run. A new `cmd/cronfoundry/runner_http.go` wraps P1's runner package with HTTP-fetched inputs. `cmd/cronfoundry/serve.go` wires the three loops + lifecycle.

**Tech Stack:**
- Go 1.25+ (unchanged)
- `github.com/golang-jwt/jwt/v5` — per-run JWTs (already in go.mod from P2b)
- `github.com/robfig/cron/v3` — cron parsing (new dep)
- Stdlib `net/http`, `os/exec`, `os/signal`, `log/slog`
- `github.com/jackc/pgx/v5`, `internal/db/gen` — schedule/run DB access
- P1's `internal/runner` package — reused as a library

---

## File Structure (locked in upfront)

```
cronfoundry/
├── cmd/cronfoundry/
│   ├── main.go                       # MODIFY — register `serve` + `runner` subcommands
│   ├── serve.go                      # NEW — wires sync + scheduler + API in one process
│   ├── serve_test.go                 # NEW — smoke test: serve boots, /healthz responds
│   ├── runner_http.go                # NEW — `cronfoundry runner --run-id` HTTP-client mode
│   └── runner_http_test.go           # NEW
├── internal/
│   ├── token/
│   │   ├── jwt.go                    # NEW — Sign(claims)/Verify(bearer) with HKDF-derived key
│   │   └── jwt_test.go
│   ├── cloud/
│   │   ├── dispatcher.go             # NEW — JobDispatcher interface + DispatchSpec
│   │   ├── subprocess.go             # NEW — SubprocessDispatcher
│   │   └── subprocess_test.go
│   ├── api/
│   │   ├── server.go                 # NEW — NewServer(Deps) *http.Server, route registration
│   │   ├── auth.go                   # NEW — bearer-extract middleware + RunClaims guard
│   │   ├── auth_test.go
│   │   ├── run_context.go            # NEW — GET /internal/runs/{id}/context handler
│   │   ├── run_context_test.go
│   │   ├── secrets.go                # NEW — GET /internal/secrets?names=...
│   │   ├── secrets_test.go
│   │   ├── clone_url.go              # NEW — GET /internal/repos/{id}/clone-url
│   │   ├── clone_url_test.go
│   │   ├── events.go                 # NEW — POST /internal/runs/{id}/events (batched)
│   │   ├── events_test.go
│   │   ├── finalize.go               # NEW — POST /internal/runs/{id}/finalize
│   │   ├── finalize_test.go
│   │   └── trigger.go                # NEW — POST /internal/schedules/{id}/run-now (operator-triggered)
│   ├── db/queries/
│   │   ├── run.sql                   # NEW — InsertRun, SetRunRunning, FinalizeRun, GetRunContext, ListActiveRunsForSchedule, OrphanSweep
│   │   ├── run_event.sql             # NEW — InsertRunEvent (batched)
│   │   └── schedule.sql              # MODIFY — add UpdateScheduleNextFireAt, ListDueSchedules
│   ├── db/gen/                       # REGENERATE via `make sqlc`
│   └── scheduler/
│       ├── tick.go                   # NEW — Tick(ctx, Deps) runs one pass
│       ├── tick_test.go
│       ├── overlap.go                # NEW — `skip`/`queue`/`concurrent` decision
│       ├── overlap_test.go
│       ├── cron.go                   # NEW — cron-expression → next time via robfig/cron/v3
│       ├── cron_test.go
│       ├── loop.go                   # NEW — Loop(ctx, Deps) — 30s ticker, calls Tick, supervises shutdown
│       └── loop_test.go
```

P1's `cmd/runner/` (standalone binary) stays exactly as-is. P2c does NOT merge it into the new `cronfoundry` binary — that's an explicit non-goal. The new `cronfoundry runner` subcommand is the HTTP-mode equivalent.

### Responsibilities

- `internal/token/` — sign/verify per-run JWTs. Signing key = HKDF(`MASTER_KEY`, "cronfoundry:run-jwt"). Claims: `run_id`, `org_id`, `exp`, `secret_refs []string`.
- `internal/cloud/dispatcher.go` — `JobDispatcher.Dispatch(ctx, spec) (Handle, error)`. `DispatchSpec` carries runner binary path, env, args. `Handle.Wait() error` for supervision.
- `internal/cloud/subprocess.go` — `SubprocessDispatcher` via `exec.CommandContext`. Stdout/stderr routed to slog.
- `internal/api/server.go` — `NewServer(Deps) *http.Server`. Binds to `127.0.0.1:8080` by default.
- `internal/api/auth.go` — extracts bearer token, verifies JWT, attaches claims to request context.
- `internal/api/{run_context,secrets,clone_url,events,finalize,trigger}.go` — one handler per endpoint, ≤ 100 lines each.
- `internal/scheduler/tick.go` — one pass: query due, insert runs, dispatch.
- `internal/scheduler/overlap.go` — decides skip/queue/concurrent for a newly-inserted run.
- `internal/scheduler/cron.go` — wraps robfig/cron/v3 parser, returns `next(now, expr, tz) (time.Time, error)`.
- `internal/scheduler/loop.go` — long-running goroutine; SIGTERM-aware.
- `cmd/cronfoundry/serve.go` — builds Deps once, starts all three loops, registers signal handler for graceful shutdown.
- `cmd/cronfoundry/runner_http.go` — reads `CRONFOUNDRY_API_URL` + `CRONFOUNDRY_RUN_ID` + `CRONFOUNDRY_RUN_TOKEN`, fetches context, calls P1 `runner.Run`, streams events back.

---

## Task 1: Per-run JWT sign/verify

**Files:**
- Create: `internal/token/jwt.go`
- Create: `internal/token/jwt_test.go`

- [ ] **Step 1: Failing test**

```go
// internal/token/jwt_test.go
package token

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func randomMaster(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

func TestSignAndVerify_RoundTrip(t *testing.T) {
	signer := New(randomMaster(t))
	runID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	tok, hash, err := signer.Sign(RunClaims{
		RunID:      runID,
		OrgID:      orgID,
		SecretRefs: []string{"slack_webhook", "openai_key"},
		ExpiresAt:  time.Now().Add(10 * time.Minute),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.NotEmpty(t, hash)
	assert.Len(t, hash, 64, "hash should be hex-encoded sha256 (64 chars)")

	claims, err := signer.Verify(tok)
	require.NoError(t, err)
	assert.Equal(t, runID, claims.RunID)
	assert.Equal(t, orgID, claims.OrgID)
	assert.ElementsMatch(t, []string{"slack_webhook", "openai_key"}, claims.SecretRefs)
}

func TestVerify_RejectsExpired(t *testing.T) {
	signer := New(randomMaster(t))
	tok, _, err := signer.Sign(RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(-1 * time.Second),
	})
	require.NoError(t, err)
	_, err = signer.Verify(tok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestVerify_RejectsDifferentKey(t *testing.T) {
	a := New(randomMaster(t))
	b := New(randomMaster(t))

	tok, _, err := a.Sign(RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	_, err = b.Verify(tok)
	require.Error(t, err)
}

func TestHashToken_IsStable(t *testing.T) {
	signer := New(randomMaster(t))
	assert.Equal(t, signer.HashToken("same"), signer.HashToken("same"))
	assert.NotEqual(t, signer.HashToken("a"), signer.HashToken("b"))
}
```

- [ ] **Step 2: Run — confirm undefined**

```bash
go test ./internal/token/... -v
```

- [ ] **Step 3: Implement `internal/token/jwt.go`**

```go
// Package token mints and verifies per-run bearer JWTs. The signing key is
// derived from the process-wide master key via HKDF so it's distinct from
// any other HMAC keys we might derive in the future.
package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"
)

const hkdfInfo = "cronfoundry:run-jwt"

// RunClaims are the fields a per-run JWT carries.
type RunClaims struct {
	RunID      uuid.UUID
	OrgID      uuid.UUID
	SecretRefs []string
	ExpiresAt  time.Time
}

// Signer signs and verifies RunClaims tokens.
type Signer struct {
	key []byte // HMAC key derived via HKDF from the master key
}

// New derives a signing key from the 32-byte master key and returns a Signer.
func New(master []byte) *Signer {
	h := hkdf.New(sha256.New, master, nil, []byte(hkdfInfo))
	key := make([]byte, 32)
	_, _ = h.Read(key)
	return &Signer{key: key}
}

// Sign returns (compact JWT, sha256-hex-hash) for the given claims.
// The hash is what the server stores in run.runner_token_hash for O(1) lookup
// and to prevent replay if the DB is compromised.
func (s *Signer) Sign(c RunClaims) (tok, hash string, err error) {
	claims := jwtv5.MapClaims{
		"run_id":      c.RunID.String(),
		"org_id":      c.OrgID.String(),
		"secret_refs": c.SecretRefs,
		"exp":         c.ExpiresAt.Unix(),
	}
	jwt := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims)
	signed, err := jwt.SignedString(s.key)
	if err != nil {
		return "", "", fmt.Errorf("token: sign: %w", err)
	}
	return signed, s.HashToken(signed), nil
}

// Verify parses + cryptographically verifies a bearer token, returning its
// claims or an error. Expired tokens produce an error whose message contains
// "expired".
func (s *Signer) Verify(bearer string) (RunClaims, error) {
	parsed, err := jwtv5.Parse(bearer, func(t *jwtv5.Token) (interface{}, error) {
		return s.key, nil
	}, jwtv5.WithValidMethods([]string{"HS256"}))
	if err != nil {
		if errors.Is(err, jwtv5.ErrTokenExpired) {
			return RunClaims{}, fmt.Errorf("token: expired")
		}
		return RunClaims{}, fmt.Errorf("token: verify: %w", err)
	}
	claims, ok := parsed.Claims.(jwtv5.MapClaims)
	if !ok || !parsed.Valid {
		return RunClaims{}, fmt.Errorf("token: verify: invalid claims")
	}

	var out RunClaims
	runStr, _ := claims["run_id"].(string)
	orgStr, _ := claims["org_id"].(string)
	if out.RunID, err = uuid.Parse(runStr); err != nil {
		return RunClaims{}, fmt.Errorf("token: verify: run_id: %w", err)
	}
	if out.OrgID, err = uuid.Parse(orgStr); err != nil {
		return RunClaims{}, fmt.Errorf("token: verify: org_id: %w", err)
	}
	if expF, ok := claims["exp"].(float64); ok {
		out.ExpiresAt = time.Unix(int64(expF), 0)
	}
	if raw, ok := claims["secret_refs"].([]interface{}); ok {
		out.SecretRefs = make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out.SecretRefs = append(out.SecretRefs, s)
			}
		}
	}
	return out, nil
}

// HashToken returns the hex-encoded sha256 of a bearer token. Stable across
// calls; deterministic for the same input.
func (s *Signer) HashToken(tok string) string {
	h := hmac.New(sha256.New, nil) // keyless sha256 via hmac.New(nil)
	h.Write([]byte(tok))
	return hex.EncodeToString(h.Sum(nil))
}
```

Note: `hmac.New(sha256.New, nil)` is deliberate — we want a plain sha256 of the token, not a keyed HMAC, since the token itself is already tied to the key and the hash is just a cheap DB index. Using the `hmac` API avoids importing `sha256` separately just for `Sum`.

Actually simpler: use `sha256.Sum256` directly. Rewrite `HashToken`:

```go
import "crypto/sha256"

func (s *Signer) HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Add `golang.org/x/crypto` dep if missing**

```bash
go get golang.org/x/crypto/hkdf@latest
```

- [ ] **Step 5: Run tests — all 4 PASS**

```bash
go test ./internal/token/... -v
go vet ./...
```

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/token/
git commit -m "feat(token): per-run JWT signer + verifier with HKDF-derived key"
```

---

## Task 2: Add sqlc queries for run + schedule operations

**Files:**
- Create: `internal/db/queries/run.sql`
- Create: `internal/db/queries/run_event.sql`
- Modify: `internal/db/queries/schedule.sql` (add `UpdateScheduleNextFireAt`, `ListDueSchedules`)
- Regenerate: `internal/db/gen/*.go`

- [ ] **Step 1: Write `internal/db/queries/run.sql`**

```sql
-- name: InsertRun :one
-- Idempotent insertion for scheduled fires (ON CONFLICT on the partial unique
-- index run(schedule_id, fire_time) WHERE fire_time IS NOT NULL). Manual runs
-- always have fire_time=NULL and pass through without collision.
INSERT INTO run (
    org_id, schedule_id, skill_sha, fire_time, status, fire_reason, actor,
    runner_token_hash
)
VALUES ($1, $2, $3, $4, 'pending', $5, $6, $7)
ON CONFLICT (schedule_id, fire_time) DO NOTHING
RETURNING *;

-- name: GetRun :one
SELECT *
FROM run
WHERE id = $1;

-- name: GetRunForContext :one
-- Returns the run + its schedule + skill + repo so the runner can assemble
-- its full context in one query.
SELECT r.*,
       s.cron, s.timezone, s.provider, s.model,
       s.llm_secret_ref, s.llm_endpoint, s.llm_deployment,
       s.destinations_json, s.writeback_json, s.env_json,
       sk.path AS skill_path, sk.frontmatter_json,
       rc.owner, rc.name AS repo_name, rc.default_branch, rc.github_app_install_id
FROM run r
JOIN schedule s        ON s.id = r.schedule_id
JOIN skill sk          ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.id = $1;

-- name: SetRunRunning :one
UPDATE run
SET status       = 'running',
    started_at   = now(),
    runner_pid   = $2
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: FinalizeRun :one
UPDATE run
SET status               = $2,
    finished_at          = now(),
    duration_ms          = $3,
    tokens_in            = $4,
    tokens_out           = $5,
    cost_cents           = $6,
    error_kind           = $7,
    error_msg            = $8,
    writeback_commit_sha = $9
WHERE id = $1
RETURNING *;

-- name: ListActiveRunsForSchedule :many
-- Used for overlap-policy decisions. Returns runs in non-terminal states.
SELECT *
FROM run
WHERE schedule_id = $1
  AND status IN ('pending', 'running')
ORDER BY created_at ASC;

-- name: DeleteRun :exec
-- For `skip` overlap policy — discards the freshly-inserted pending row.
DELETE FROM run WHERE id = $1;

-- name: OrphanSweep :execrows
-- Marks any non-terminal run as failed if it's been sitting longer than the
-- schedule's timeout + 5-minute grace, to recover from crashed-runner and
-- restart-during-run cases.
UPDATE run
SET status     = 'failed',
    error_kind = 'shutdown',
    error_msg  = COALESCE(error_msg, 'orphan sweep: run exceeded timeout'),
    finished_at = now()
FROM schedule s
WHERE run.schedule_id = s.id
  AND run.status IN ('pending','running')
  AND now() - COALESCE(run.started_at, run.created_at) > (s.timeout_sec + 300) * interval '1 second';
```

- [ ] **Step 2: Write `internal/db/queries/run_event.sql`**

```sql
-- name: InsertRunEvent :exec
INSERT INTO run_event (run_id, level, event_type, payload_json)
VALUES ($1, $2, $3, $4);

-- name: ListRunEvents :many
SELECT *
FROM run_event
WHERE run_id = $1
ORDER BY ts ASC, id ASC;
```

- [ ] **Step 3: Modify `internal/db/queries/schedule.sql`** — append at the bottom:

```sql
-- name: ListDueSchedules :many
-- Returns schedules ready to fire: enabled AND next_fire_at <= now. Ordered
-- by next_fire_at so we dispatch oldest-due first.
SELECT *
FROM schedule
WHERE enabled = true
  AND next_fire_at IS NOT NULL
  AND next_fire_at <= now()
ORDER BY next_fire_at ASC;

-- name: UpdateScheduleNextFireAt :exec
UPDATE schedule
SET next_fire_at = $2,
    updated_at   = now()
WHERE id = $1;
```

- [ ] **Step 4: Regenerate sqlc**

```bash
make sqlc
```

Expected: `internal/db/gen/run.sql.go`, `run_event.sql.go` appear. `schedule.sql.go` gains new methods. `go build ./...` clean, `go vet ./...` clean.

- [ ] **Step 5: Commit**

```bash
git add internal/db/queries/ internal/db/gen/
git commit -m "feat(db): add sqlc queries for run lifecycle and schedule tick"
```

---

## Task 3: Cron next-fire helper

**Files:**
- Create: `internal/scheduler/cron.go`
- Create: `internal/scheduler/cron_test.go`

- [ ] **Step 1: Add robfig/cron dep**

```bash
go get github.com/robfig/cron/v3@latest
```

- [ ] **Step 2: Failing test**

```go
// internal/scheduler/cron_test.go
package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNextFire_EverMinute(t *testing.T) {
	base := time.Date(2026, 4, 20, 9, 0, 30, 0, time.UTC)
	next, err := NextFire("* * * * *", "UTC", base)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 20, 9, 1, 0, 0, time.UTC), next)
}

func TestNextFire_HandlesTimezone(t *testing.T) {
	// 09:00 Pacific in April = 16:00 UTC (PDT, UTC-7).
	base := time.Date(2026, 4, 20, 16, 0, 0, 0, time.UTC).Add(-time.Minute) // 15:59 UTC == 08:59 PT
	next, err := NextFire("0 9 * * *", "America/Los_Angeles", base)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 20, 16, 0, 0, 0, time.UTC), next)
}

func TestNextFire_BadExpr(t *testing.T) {
	_, err := NextFire("nonsense", "UTC", time.Now())
	require.Error(t, err)
}

func TestNextFire_BadTimezone(t *testing.T) {
	_, err := NextFire("* * * * *", "Not/A/Zone", time.Now())
	require.Error(t, err)
}
```

- [ ] **Step 3: Implement `internal/scheduler/cron.go`**

```go
// Package scheduler implements the tick loop that turns due schedules into
// run rows and dispatches them via the JobDispatcher.
package scheduler

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// NextFire returns the first time >= base at which the given cron expression
// will fire in the given IANA timezone.
func NextFire(expr, tz string, base time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: NextFire: load timezone %q: %w", tz, err)
	}
	schedule, err := parser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: NextFire: parse %q: %w", expr, err)
	}
	return schedule.Next(base.In(loc)).UTC(), nil
}
```

- [ ] **Step 4: Run tests, confirm 4 PASS. `go vet` clean.**

```bash
go test ./internal/scheduler/... -v
```

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/scheduler/cron.go internal/scheduler/cron_test.go
git commit -m "feat(scheduler): cron expression + timezone → next fire time"
```

---

## Task 4: Overlap-policy decision

**Files:**
- Create: `internal/scheduler/overlap.go`
- Create: `internal/scheduler/overlap_test.go`

- [ ] **Step 1: Failing test**

```go
// internal/scheduler/overlap_test.go
package scheduler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecide_Skip_NoActive(t *testing.T) {
	decision := Decide(PolicySkip, 0)
	assert.Equal(t, DecisionDispatch, decision)
}

func TestDecide_Skip_WithActive(t *testing.T) {
	decision := Decide(PolicySkip, 1)
	assert.Equal(t, DecisionSkip, decision)
}

func TestDecide_Queue_WithActive(t *testing.T) {
	// queue leaves the run pending; scheduler will retry next tick.
	decision := Decide(PolicyQueue, 1)
	assert.Equal(t, DecisionQueue, decision)
}

func TestDecide_Concurrent_AlwaysDispatches(t *testing.T) {
	assert.Equal(t, DecisionDispatch, Decide(PolicyConcurrent, 0))
	assert.Equal(t, DecisionDispatch, Decide(PolicyConcurrent, 5))
}

func TestDecide_UnknownPolicyDefaultsToSkip(t *testing.T) {
	assert.Equal(t, DecisionSkip, Decide(Policy("weird"), 1))
	assert.Equal(t, DecisionDispatch, Decide(Policy(""), 0))
}
```

- [ ] **Step 2: Implement `internal/scheduler/overlap.go`**

```go
package scheduler

// Policy is the overlap policy declared on a schedule.
type Policy string

const (
	PolicySkip       Policy = "skip"
	PolicyQueue      Policy = "queue"
	PolicyConcurrent Policy = "concurrent"
)

// Decision tells the scheduler what to do with a freshly-inserted pending run.
type Decision int

const (
	DecisionDispatch Decision = iota // proceed to dispatch
	DecisionSkip                     // delete the pending row; don't dispatch
	DecisionQueue                    // leave the pending row for a later tick
)

// Decide applies the overlap policy given the count of non-terminal runs for
// the same schedule (not including the row we just inserted).
//
// "skip" → dispatch only if no active runs; otherwise delete this pending row
// "queue" → always dispatch if no active runs; otherwise leave pending
// "concurrent" → always dispatch, regardless
//
// Empty/unknown policies are treated as "skip" (the safe default).
func Decide(policy Policy, activeCount int) Decision {
	switch policy {
	case PolicyConcurrent:
		return DecisionDispatch
	case PolicyQueue:
		if activeCount == 0 {
			return DecisionDispatch
		}
		return DecisionQueue
	case PolicySkip, Policy(""):
		if activeCount == 0 {
			return DecisionDispatch
		}
		return DecisionSkip
	default:
		// Unknown policy — fail closed with skip.
		if activeCount == 0 {
			return DecisionDispatch
		}
		return DecisionSkip
	}
}
```

- [ ] **Step 3: Run tests, commit**

```bash
go test ./internal/scheduler/... -v
git add internal/scheduler/overlap.go internal/scheduler/overlap_test.go
git commit -m "feat(scheduler): skip/queue/concurrent overlap-policy decisions"
```

---

## Task 5: JobDispatcher interface + SubprocessDispatcher

**Files:**
- Create: `internal/cloud/dispatcher.go`
- Create: `internal/cloud/subprocess.go`
- Create: `internal/cloud/subprocess_test.go`

- [ ] **Step 1: Write `internal/cloud/dispatcher.go`**

```go
// Package cloud defines pluggable interfaces for cloud-specific concerns —
// job dispatch, secret storage, and identity. P2 ships localhost
// implementations only; P4 will add Azure variants behind the same interfaces.
package cloud

import (
	"context"
)

// DispatchSpec describes a single job to run.
type DispatchSpec struct {
	// BinaryPath is the absolute path to the runner binary to execute.
	BinaryPath string
	// Args are passed positionally after the binary path.
	Args []string
	// Env contains additional environment variables, formatted as "KEY=VALUE".
	Env []string
}

// Handle is returned by Dispatch so the caller can observe / supervise the
// running job. In the P2 subprocess implementation this wraps *os.Process;
// in P4 it wraps an Azure Container Apps Job execution.
type Handle interface {
	// PID returns the OS process identifier for the running job, or 0 if the
	// underlying executor doesn't expose one.
	PID() int
	// Wait blocks until the job terminates. Returns the exit error (nil on
	// successful exit with code 0).
	Wait() error
	// Kill terminates the job (SIGTERM then SIGKILL on Unix).
	Kill() error
}

// JobDispatcher dispatches one-shot jobs and returns a Handle for supervision.
type JobDispatcher interface {
	Dispatch(ctx context.Context, spec DispatchSpec) (Handle, error)
}
```

- [ ] **Step 2: Failing test for SubprocessDispatcher**

```go
// internal/cloud/subprocess_test.go
package cloud

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findEcho locates a binary that always exits 0 with optional args.
func findEcho(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipping subprocess test on Windows; POSIX-specific behavior")
	}
	p, err := exec.LookPath("true")
	require.NoError(t, err)
	return p
}

func TestSubprocessDispatcher_RunsAndWaits(t *testing.T) {
	d := NewSubprocessDispatcher()
	h, err := d.Dispatch(context.Background(), DispatchSpec{
		BinaryPath: findEcho(t),
	})
	require.NoError(t, err)
	require.Positive(t, h.PID())
	require.NoError(t, h.Wait())
}

func TestSubprocessDispatcher_NonZeroExitSurfacesError(t *testing.T) {
	bin, err := exec.LookPath("false")
	require.NoError(t, err)
	d := NewSubprocessDispatcher()
	h, err := d.Dispatch(context.Background(), DispatchSpec{BinaryPath: bin})
	require.NoError(t, err)
	err = h.Wait()
	require.Error(t, err)
}

func TestSubprocessDispatcher_KillTerminates(t *testing.T) {
	bin, err := exec.LookPath("sleep")
	require.NoError(t, err)
	d := NewSubprocessDispatcher()
	h, err := d.Dispatch(context.Background(), DispatchSpec{
		BinaryPath: bin,
		Args:       []string{"10"},
	})
	require.NoError(t, err)

	// Give sleep a moment to start.
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, h.Kill())
	err = h.Wait()
	require.Error(t, err, "expected non-nil exit after Kill")
}
```

- [ ] **Step 3: Implement `internal/cloud/subprocess.go`**

```go
package cloud

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// SubprocessDispatcher runs DispatchSpec jobs via os/exec. It is the P2
// localhost implementation of JobDispatcher.
type SubprocessDispatcher struct{}

// NewSubprocessDispatcher returns a ready-to-use dispatcher.
func NewSubprocessDispatcher() *SubprocessDispatcher { return &SubprocessDispatcher{} }

// Dispatch starts the job and returns a Handle.
func (d *SubprocessDispatcher) Dispatch(ctx context.Context, spec DispatchSpec) (Handle, error) {
	cmd := exec.CommandContext(ctx, spec.BinaryPath, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	// Runner stdout/stderr are captured by the scheduler; for P2c we route
	// to our own stdout/stderr so slog sees them.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cloud: subprocess: start %s: %w", spec.BinaryPath, err)
	}
	return &subprocessHandle{cmd: cmd}, nil
}

type subprocessHandle struct {
	cmd *exec.Cmd
}

func (h *subprocessHandle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *subprocessHandle) Wait() error {
	return h.cmd.Wait()
}

func (h *subprocessHandle) Kill() error {
	if h.cmd.Process == nil {
		return nil
	}
	return h.cmd.Process.Kill()
}
```

- [ ] **Step 4: Run tests, commit**

```bash
go test ./internal/cloud/... -v
git add internal/cloud/
git commit -m "feat(cloud): JobDispatcher interface + SubprocessDispatcher"
```

---

## Task 6: API server skeleton + bearer-auth middleware

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/auth.go`
- Create: `internal/api/auth_test.go`

- [ ] **Step 1: Write `internal/api/server.go`**

```go
// Package api hosts the /internal HTTP surface used by runner subprocesses
// to fetch their context, request scoped secrets, stream events, and
// finalize. No human-facing endpoints live here — that's P3's job.
package api

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/secretstore"
	"github.com/gambtho/cronfoundry/internal/token"
)

// Deps bundles everything a handler might need. Passed once at startup.
type Deps struct {
	Pool          *pgxpool.Pool
	Signer        *token.Signer
	Secrets       secretstore.SecretStore
	Installations *github.InstallationCache
}

// NewServer builds an *http.Server with all handlers registered under
// /internal/*. Bind the returned server to 127.0.0.1 via ListenAndServe.
func NewServer(addr string, deps Deps) *http.Server {
	mux := http.NewServeMux()

	// Health check is unauthenticated.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// All /internal/* routes require a valid per-run bearer token.
	auth := requireBearer(deps.Signer)

	mux.Handle("GET /internal/runs/{id}/context", auth(handleRunContext(deps)))
	mux.Handle("GET /internal/secrets", auth(handleSecrets(deps)))
	mux.Handle("GET /internal/repos/{id}/clone-url", auth(handleCloneURL(deps)))
	mux.Handle("POST /internal/runs/{id}/events", auth(handleEvents(deps)))
	mux.Handle("POST /internal/runs/{id}/finalize", auth(handleFinalize(deps)))

	// Manual trigger is unauthenticated (CLI-local). P3 will gate behind UI
	// session auth.
	mux.Handle("POST /internal/schedules/{id}/run-now", handleRunNow(deps))

	return &http.Server{Addr: addr, Handler: mux}
}

// Handler stubs for tasks 7-13; defined here so server.go compiles.
func handleRunContext(deps Deps) http.Handler { return runContextHandler{deps} }
func handleSecrets(deps Deps) http.Handler     { return secretsHandler{deps} }
func handleCloneURL(deps Deps) http.Handler    { return cloneURLHandler{deps} }
func handleEvents(deps Deps) http.Handler      { return eventsHandler{deps} }
func handleFinalize(deps Deps) http.Handler    { return finalizeHandler{deps} }
func handleRunNow(deps Deps) http.Handler      { return runNowHandler{deps} }

// Placeholder types; each task below gives one a real ServeHTTP.
type runContextHandler struct{ deps Deps }
type secretsHandler struct{ deps Deps }
type cloneURLHandler struct{ deps Deps }
type eventsHandler struct{ deps Deps }
type finalizeHandler struct{ deps Deps }
type runNowHandler struct{ deps Deps }

// Stub ServeHTTPs — each returns 501 until the per-endpoint task lands.
func (h runContextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { http.Error(w, "not implemented", http.StatusNotImplemented) }
func (h secretsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request)     { http.Error(w, "not implemented", http.StatusNotImplemented) }
func (h cloneURLHandler) ServeHTTP(w http.ResponseWriter, r *http.Request)    { http.Error(w, "not implemented", http.StatusNotImplemented) }
func (h eventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request)      { http.Error(w, "not implemented", http.StatusNotImplemented) }
func (h finalizeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request)    { http.Error(w, "not implemented", http.StatusNotImplemented) }
func (h runNowHandler) ServeHTTP(w http.ResponseWriter, r *http.Request)      { http.Error(w, "not implemented", http.StatusNotImplemented) }
```

(The per-endpoint tasks below will replace each stub with the real ServeHTTP implementation.)

- [ ] **Step 2: Failing test for auth middleware**

```go
// internal/api/auth_test.go
package api

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/token"
)

func randomMaster(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)
	return k
}

// helloHandler echoes the run_id from the claims so we can verify the
// middleware attached them correctly.
func helloHandler(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	_, _ = w.Write([]byte(claims.RunID.String()))
}

func TestRequireBearer_Accepts(t *testing.T) {
	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		OrgID:     uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	h := requireBearer(signer)(http.HandlerFunc(helloHandler))

	req := httptest.NewRequest("GET", "/internal/runs/anything", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", rr.Body.String())
}

func TestRequireBearer_Rejects_NoHeader(t *testing.T) {
	signer := token.New(randomMaster(t))
	h := requireBearer(signer)(http.HandlerFunc(helloHandler))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/internal/runs/x", nil))
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireBearer_Rejects_BadToken(t *testing.T) {
	signer := token.New(randomMaster(t))
	h := requireBearer(signer)(http.HandlerFunc(helloHandler))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/internal/runs/x", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	h.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
```

- [ ] **Step 3: Implement `internal/api/auth.go`**

```go
package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/gambtho/cronfoundry/internal/token"
)

type ctxKey int

const claimsKey ctxKey = 0

// requireBearer is a middleware that extracts the Authorization header,
// verifies it via the signer, attaches the claims to the request context,
// and calls the next handler. Invalid tokens produce 401.
func requireBearer(signer *token.Signer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, "missing bearer", http.StatusUnauthorized)
				return
			}
			claims, err := signer.Verify(strings.TrimPrefix(auth, "Bearer "))
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext returns the verified run claims attached by the
// requireBearer middleware. Zero value if the middleware wasn't applied.
func ClaimsFromContext(ctx context.Context) token.RunClaims {
	c, _ := ctx.Value(claimsKey).(token.RunClaims)
	return c
}
```

- [ ] **Step 4: Run tests, confirm 3 auth tests PASS, `go build ./...` clean (server.go compiles via the stub handlers)**

- [ ] **Step 5: Commit**

```bash
git add internal/api/server.go internal/api/auth.go internal/api/auth_test.go
git commit -m "feat(api): server skeleton + per-run bearer-auth middleware"
```

---

## Task 7: `GET /internal/runs/{id}/context` handler

**Files:**
- Create: `internal/api/run_context.go`
- Create: `internal/api/run_context_test.go`

The runner calls this to discover the skill it's supposed to execute. The handler guards that the URL's `{id}` matches the JWT's `run_id` claim (so a compromised runner can't read other runs' contexts).

- [ ] **Step 1: Failing test (integration, boots Postgres)**

```go
// internal/api/run_context_test.go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gambtho/cronfoundry/internal/db"
	"github.com/gambtho/cronfoundry/internal/token"
)

func bootPG(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("cf"),
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
	return pool, func() { pool.Close(); _ = c.Terminate(context.Background()) }
}

// seedRun creates an organization → repo_connection → skill → schedule → run
// chain and returns the run UUID as pgtype.UUID + uuid.UUID.
func seedRun(t *testing.T, pool *pgxpool.Pool) (runID uuid.UUID, orgID pgtype.UUID) {
	t.Helper()
	ctx := context.Background()

	var orgPG pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgPG))
	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgPG).Scan(&repoID))
	var skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha1', '{}'::jsonb) RETURNING id`, orgPG, repoID).Scan(&skillID))
	var schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		 VALUES ($1, $2, 's', '* * * * *', 'openai', 'gpt-4o-mini', '[]'::jsonb) RETURNING id`,
		orgPG, skillID).Scan(&schedID))

	var runPG pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash)
		 VALUES ($1, $2, 'sha1', 'pending', 'manual', 'hash') RETURNING id`,
		orgPG, schedID).Scan(&runPG))

	return uuid.UUID(runPG.Bytes), orgPG
}

func TestRunContext_ReturnsContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/internal/runs/"+runID.String()+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, "openai", body["provider"])
	assert.Equal(t, "gpt-4o-mini", body["model"])
	assert.Equal(t, "skills/a", body["skill_path"])
	assert.Equal(t, "o/r", body["repo"])
}

func TestRunContext_RejectsMismatchedRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	actualRun, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	// Sign a token for a DIFFERENT run_id.
	otherRun := uuid.New()
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     otherRun,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// Request the ACTUAL run's context with a token for a DIFFERENT run.
	req, _ := http.NewRequest("GET", ts.URL+"/internal/runs/"+actualRun.String()+"/context", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
```

- [ ] **Step 2: Implement `internal/api/run_context.go`**

Replace the stub `runContextHandler` in `server.go` with a real implementation in a new file:

```go
// internal/api/run_context.go
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// RunContext is the JSON returned to the runner.
type RunContext struct {
	RunID            string          `json:"run_id"`
	OrgID            string          `json:"org_id"`
	SkillPath        string          `json:"skill_path"`
	SkillSha         string          `json:"skill_sha"`
	Repo             string          `json:"repo"`       // "owner/name"
	RepoID           string          `json:"repo_id"`
	DefaultBranch    string          `json:"default_branch"`
	InstallationID   int64           `json:"installation_id"`
	Provider         string          `json:"provider"`
	Model            string          `json:"model"`
	LLMSecretRef     *string         `json:"llm_secret_ref,omitempty"`
	LLMEndpoint      *string         `json:"llm_endpoint,omitempty"`
	LLMDeployment    *string         `json:"llm_deployment,omitempty"`
	Destinations     json.RawMessage `json:"destinations"`
	Writeback        json.RawMessage `json:"writeback,omitempty"`
	Env              json.RawMessage `json:"env"`
	FrontmatterJSON  json.RawMessage `json:"frontmatter"`
}

// ServeHTTP replaces the stub in server.go.
func (h runContextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlRunID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}

	claims := ClaimsFromContext(r.Context())
	if claims.RunID != urlRunID {
		http.Error(w, "token run_id does not match URL", http.StatusForbidden)
		return
	}

	q := dbgen.New(h.deps.Pool)
	row, err := q.GetRunForContext(r.Context(), pgtype.UUID{Bytes: urlRunID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("load run: %v", err), http.StatusInternalServerError)
		return
	}

	out := RunContext{
		RunID:           urlRunID.String(),
		OrgID:           uuid.UUID(row.OrgID.Bytes).String(),
		SkillPath:       row.SkillPath,
		SkillSha:        row.SkillSha,
		Repo:            row.Owner + "/" + row.RepoName,
		RepoID:          uuid.UUID(row.RepoID.Bytes).String(),
		DefaultBranch:   row.DefaultBranch,
		InstallationID:  row.GithubAppInstallID,
		Provider:        row.Provider,
		Model:           row.Model,
		LLMSecretRef:    row.LlmSecretRef,
		LLMEndpoint:     row.LlmEndpoint,
		LLMDeployment:   row.LlmDeployment,
		Destinations:    row.DestinationsJson,
		Writeback:       row.WritebackJson,
		Env:             row.EnvJson,
		FrontmatterJSON: row.FrontmatterJson,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		// Body already partially written; log at caller level — here we
		// can't meaningfully recover.
		_ = err
	}
}
```

**Note:** the `GetRunForContext` query's return row exposes `RepoID` as `pgtype.UUID`, not surfaced in this plan's earlier query. The implementer should verify the generated struct and adjust the joined columns. If `RepoID` isn't on the generated row, swap the SELECT to `SELECT r.*, ..., sk.repo_id AS skill_repo_id`.

Also remove the stub `ServeHTTP` from `server.go` when the real implementation lands (or keep it and delete from server.go — just don't double-define).

- [ ] **Step 3: Tests pass, commit**

```bash
go test ./internal/api/... -run TestRunContext -v
git add internal/api/run_context.go internal/api/run_context_test.go internal/api/server.go
git commit -m "feat(api): GET /internal/runs/{id}/context"
```

---

## Task 8: `GET /internal/secrets` with scoped secret_refs

- [ ] **Step 1: Failing test**

```go
// internal/api/secrets_test.go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/secretstore"
	"github.com/gambtho/cronfoundry/internal/token"
)

func TestSecrets_ScopedToClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))

	master := randomMaster(t)
	store := secretstore.NewEnvelopePostgresStore(pool, orgID, master)
	require.NoError(t, store.Put(context.Background(), "allowed", "value-A"))
	require.NoError(t, store.Put(context.Background(), "forbidden", "value-F"))

	signer := token.New(master)
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:      uuid.New(),
		OrgID:      uuid.UUID(orgID.Bytes),
		SecretRefs: []string{"allowed"},
		ExpiresAt:  time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	srv := NewServer("127.0.0.1:0", Deps{
		Pool:    pool,
		Signer:  signer,
		Secrets: store,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// Allowed secret returns value.
	req, _ := http.NewRequest("GET", ts.URL+"/internal/secrets?names=allowed", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "value-A", body["allowed"])

	// Forbidden secret → 403.
	req2, _ := http.NewRequest("GET", ts.URL+"/internal/secrets?names=forbidden", nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	resp2, err := ts.Client().Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp2.StatusCode)
}
```

- [ ] **Step 2: Implement `internal/api/secrets.go`**

```go
package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (h secretsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims := ClaimsFromContext(r.Context())
	allowed := make(map[string]struct{}, len(claims.SecretRefs))
	for _, n := range claims.SecretRefs {
		allowed[n] = struct{}{}
	}

	namesParam := r.URL.Query().Get("names")
	if namesParam == "" {
		http.Error(w, "missing names query parameter", http.StatusBadRequest)
		return
	}
	names := strings.Split(namesParam, ",")

	out := make(map[string]string, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if _, ok := allowed[name]; !ok {
			http.Error(w, "secret not in token scope: "+name, http.StatusForbidden)
			return
		}
		val, err := h.deps.Secrets.Get(r.Context(), name)
		if err != nil {
			http.Error(w, "load secret: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out[name] = val
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
```

- [ ] **Step 3: Run tests, commit**

```bash
go test ./internal/api/... -run TestSecrets -v
git add internal/api/secrets.go internal/api/secrets_test.go
git commit -m "feat(api): GET /internal/secrets with token-scoped allowlist"
```

---

## Task 9: `GET /internal/repos/{id}/clone-url`

The runner uses this to get a short-lived authenticated HTTPS clone URL (so it doesn't need the GitHub App private key itself).

- [ ] **Step 1: Test**

```go
// internal/api/clone_url_test.go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/githubtest"
	"github.com/gambtho/cronfoundry/internal/token"
)

func TestCloneURL_MintsURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	// Seed org + repo.
	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))
	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 42, 'acme', 'widgets', 'main') RETURNING id`, orgID).Scan(&repoID))

	// Fake GitHub token-exchange endpoint.
	tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_xyz",
			"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
	defer tokSrv.Close()

	privPEM, _ := githubtest.MustPrivateKey(t)
	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID:      "1",
		PrivateKey: privPEM,
		BaseURL:    tokSrv.URL,
		HTTPClient: tokSrv.Client(),
	})

	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	srv := NewServer("127.0.0.1:0", Deps{
		Pool:          pool,
		Signer:        signer,
		Installations: cache,
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("GET",
		ts.URL+"/internal/repos/"+uuid.UUID(repoID.Bytes).String()+"/clone-url", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Contains(t, body["url"], "x-access-token:ghs_xyz@github.com/acme/widgets.git")
}
```

- [ ] **Step 2: Implement `internal/api/clone_url.go`**

```go
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func (h cloneURLHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repoID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid repo id", http.StatusBadRequest)
		return
	}
	q := dbgen.New(h.deps.Pool)
	row, err := q.GetRepoConnection(r.Context(), pgtype.UUID{Bytes: repoID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "repo not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("load repo: %v", err), http.StatusInternalServerError)
		return
	}

	tok, err := h.deps.Installations.Token(r.Context(), row.GithubAppInstallID)
	if err != nil {
		http.Error(w, "mint install token: "+err.Error(), http.StatusBadGateway)
		return
	}

	url := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", tok, row.Owner, row.Name)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": url})
}
```

- [ ] **Step 3: Test, commit**

```bash
go test ./internal/api/... -run TestCloneURL -v
git add internal/api/clone_url.go internal/api/clone_url_test.go
git commit -m "feat(api): GET /internal/repos/{id}/clone-url"
```

---

## Task 10: `POST /internal/runs/{id}/events`

Batched event stream from the runner. Body: `{"events": [{"type": "...", "level": "...", "payload": {...}}, ...]}`.

- [ ] **Step 1: Test**

```go
// internal/api/events_test.go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/token"
)

func TestEvents_PersistsBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := bootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)

	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]any{
		"events": []map[string]any{
			{"type": "llm.start", "level": "info", "payload": map[string]string{"model": "gpt-4o-mini"}},
			{"type": "publish.slack.ok", "level": "info", "payload": map[string]string{"http": "200"}},
		},
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/events", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	var count int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM run_event WHERE run_id = $1`, runID).Scan(&count))
	assert.Equal(t, 2, count)
}
```

- [ ] **Step 2: Implement `internal/api/events.go`**

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

type eventBatch struct {
	Events []struct {
		Type    string          `json:"type"`
		Level   string          `json:"level"`
		Payload json.RawMessage `json:"payload"`
	} `json:"events"`
}

func (h eventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlRunID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	if ClaimsFromContext(r.Context()).RunID != urlRunID {
		http.Error(w, "token run_id mismatch", http.StatusForbidden)
		return
	}

	var batch eventBatch
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}

	q := dbgen.New(h.deps.Pool)
	for _, ev := range batch.Events {
		level := ev.Level
		if level == "" {
			level = "info"
		}
		if level != "info" && level != "warn" && level != "error" {
			http.Error(w, "invalid level: "+level, http.StatusBadRequest)
			return
		}
		payload := ev.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("{}")
		}
		if err := q.InsertRunEvent(r.Context(), dbgen.InsertRunEventParams{
			RunID:       pgtype.UUID{Bytes: urlRunID, Valid: true},
			Level:       level,
			EventType:   ev.Type,
			PayloadJson: payload,
		}); err != nil {
			http.Error(w, "persist: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 3: Test, commit**

```bash
go test ./internal/api/... -run TestEvents -v
git add internal/api/events.go internal/api/events_test.go
git commit -m "feat(api): POST /internal/runs/{id}/events — batched timeline"
```

---

## Task 11: `POST /internal/runs/{id}/finalize`

Body:

```json
{
  "status": "succeeded",
  "duration_ms": 1234,
  "tokens_in": 400,
  "tokens_out": 120,
  "cost_cents": 1,
  "writeback_commit_sha": "abcd..." ,
  "error_kind": null,
  "error_msg": null
}
```

Valid statuses: `succeeded`, `partial_failure`, `failed`.

- [ ] **Step 1: Test + implementation follow the pattern of Tasks 10. Signature details:**

```go
func (h finalizeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Enforce claim matches URL id (same pattern as events)
	// Parse body, validate status in allowed set
	// Call q.FinalizeRun(...) passing nullable *string for error_kind/msg/writeback_sha
	// 204 on success
}
```

Allowed statuses assert + a mismatched-claim test. Commit:

```bash
git add internal/api/finalize.go internal/api/finalize_test.go
git commit -m "feat(api): POST /internal/runs/{id}/finalize"
```

**The implementer should write full test + implementation following the same shape as T10. Task budget: ~60 minutes.**

---

## Task 12: `POST /internal/schedules/{id}/run-now`

Operator-triggered manual fire. Inserts a `run` row with `fire_reason='manual'`, `fire_time=NULL`. Returns the run ID so CLI callers can follow it.

- [ ] **Step 1: Test + implementation.** Body:

```json
{ "actor": "alice" }
```

Response:

```json
{ "run_id": "..." }
```

Scheduler's loop picks up the pending run on next tick. (Alternative: dispatch immediately. For MVP, tick-based is simpler and uniform.)

- [ ] **Step 2: Commit**

```bash
git add internal/api/trigger.go internal/api/trigger_test.go
git commit -m "feat(api): POST /internal/schedules/{id}/run-now — manual trigger"
```

---

## Task 13: Scheduler tick — the heart of the loop

**Files:**
- Create: `internal/scheduler/tick.go`
- Create: `internal/scheduler/tick_test.go`

- [ ] **Step 1: Tick signature**

```go
// Tick runs one pass: query due schedules, insert pending runs, advance
// next_fire_at, apply overlap policy, dispatch.
//
// Returns (dispatched, skipped, queued int, err error).
func Tick(ctx context.Context, deps Deps) (Stats, error) {
    // 1. Call ListDueSchedules
    // 2. For each schedule:
    //    a. Begin tx
    //    b. Compute next_fire_at via NextFire(s.Cron, s.Timezone, s.NextFireAt)
    //    c. InsertRun (ON CONFLICT DO NOTHING). If not inserted, commit + continue.
    //    d. UpdateScheduleNextFireAt
    //    e. Commit
    //    f. Apply overlap policy:
    //       - Count ListActiveRunsForSchedule minus this one
    //       - Decide(policy, activeCount)
    //       - DecisionSkip: q.DeleteRun(thisRunID); stats.Skipped++
    //       - DecisionQueue: leave pending; stats.Queued++
    //       - DecisionDispatch: sign JWT, update runner_token_hash, dispatch via deps.Dispatcher
    //         stats.Dispatched++
    // 3. Run OrphanSweep
    // 4. Return stats
}
```

```go
// Deps for scheduler (distinct from api.Deps).
type Deps struct {
    Pool       *pgxpool.Pool
    Signer     *token.Signer
    Dispatcher cloud.JobDispatcher
    APIBaseURL string // "http://127.0.0.1:8080/internal" — runner env
    RunnerBinary string // os.Executable() by default
}

type Stats struct {
    Dispatched int
    Skipped    int
    Queued     int
}
```

- [ ] **Step 2: Test via integration (seed a due schedule, call Tick, assert run row exists + status=pending, assert dispatch was invoked via a mock Dispatcher).** ~90 min to write + debug.

- [ ] **Step 3: Commit**

```bash
git add internal/scheduler/tick.go internal/scheduler/tick_test.go
git commit -m "feat(scheduler): Tick — due scheduling with overlap policies"
```

---

## Task 14: Scheduler loop with graceful shutdown

**Files:**
- Create: `internal/scheduler/loop.go`
- Create: `internal/scheduler/loop_test.go`

- [ ] **Step 1: Loop signature**

```go
// Loop runs Tick on a 30-second ticker until ctx is cancelled. Errors from
// Tick are logged but do not stop the loop.
func Loop(ctx context.Context, deps Deps) error {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    // Run once immediately so tests (or operators) don't wait 30s.
    if _, err := Tick(ctx, deps); err != nil {
        slog.Error("scheduler: initial tick failed", "err", err)
    }
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            stats, err := Tick(ctx, deps)
            if err != nil {
                slog.Error("scheduler: tick failed", "err", err)
                continue
            }
            slog.Info("scheduler: tick", "dispatched", stats.Dispatched, "skipped", stats.Skipped, "queued", stats.Queued)
        }
    }
}
```

- [ ] **Step 2: Test with a 100ms-interval variant + a context that cancels after 250ms, assert Loop returns ctx.Err and called Tick ≥2 times.** Use a test Deps that wraps the real Tick but logs calls.

Actually — expose `Loop` to accept a cadence so tests don't wait 30s:

```go
func Loop(ctx context.Context, cadence time.Duration, deps Deps) error { ... }
```

- [ ] **Step 3: Commit**

```bash
git add internal/scheduler/loop.go internal/scheduler/loop_test.go
git commit -m "feat(scheduler): Loop — cadenced tick with slog"
```

---

## Task 15: Runner HTTP-mode subcommand

**Files:**
- Create: `cmd/cronfoundry/runner_http.go`
- Create: `cmd/cronfoundry/runner_http_test.go`

The runner does:

1. Parse env: `CRONFOUNDRY_API_URL`, `CRONFOUNDRY_RUN_ID`, `CRONFOUNDRY_RUN_TOKEN`.
2. GET `/internal/runs/{id}/context` with bearer auth.
3. GET `/internal/secrets?names=<all needed>` — build from the destinations' secret refs + llm_secret_ref.
4. GET `/internal/repos/{id}/clone-url` for the install-tokened HTTPS URL.
5. Invoke P1's `runner.Run` with inputs assembled from the above.
6. POST events during execution (batched).
7. POST `/internal/runs/{id}/finalize` at the end.

- [ ] **Step 1: Skeleton**

```go
// cmd/cronfoundry/runner_http.go
package main

import (
	"github.com/spf13/cobra"
)

func newRunnerCmd() *cobra.Command {
	var runID string
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Execute a single run in HTTP mode (fetches context from API)",
		Hidden: true, // not for operator use; scheduler invokes this
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRunnerHTTP(cmd.Context(), runID)
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "run UUID (also read from CRONFOUNDRY_RUN_ID)")
	return cmd
}

func runRunnerHTTP(ctx context.Context, runIDFlag string) error {
    // 1. Resolve run_id (flag or env)
    // 2. Build HTTP client with bearer auth
    // 3. Fetch context → build runner.RunInput
    // 4. Extract secret names, fetch secrets in one call
    // 5. Fetch clone URL
    // 6. Assemble P1 runner.Deps; call runner.Run
    // 7. Post events on progress (use a runner event hook if P1 exposes one;
    //    otherwise batch from the result)
    // 8. POST finalize with the result
    return nil
}
```

- [ ] **Step 2: Full E2E test.** This is the integration test that validates the whole round-trip: boot API + seed run + spawn runner subprocess with env vars → asserts run finalized with status=succeeded (using a fake LLM + discord webhook à la P1 smoke fixture).

- [ ] **Step 3: Register in `main.go`**

In `cmd/cronfoundry/main.go`, add:

```go
root.AddCommand(newRunnerCmd())
root.AddCommand(newServeCmd()) // T17
```

- [ ] **Step 4: Commit**

```bash
git add cmd/cronfoundry/main.go cmd/cronfoundry/runner_http.go cmd/cronfoundry/runner_http_test.go
git commit -m "feat(runner): HTTP-mode subcommand that fetches run context from API"
```

---

## Task 16: Orphan sweep on startup

**Files:**
- Create: `internal/scheduler/sweep.go`
- Create: `internal/scheduler/sweep_test.go`

Simple wrapper around `q.OrphanSweep`. Called once by `serve` at startup and periodically from `Loop` (already added as the last step of `Tick` in Task 13, but exposing a standalone function is useful for serve's boot sequence).

```go
func SweepOrphans(ctx context.Context, deps Deps) (int64, error) {
    q := dbgen.New(deps.Pool)
    affected, err := q.OrphanSweep(ctx)
    if err != nil {
        return 0, fmt.Errorf("scheduler: sweep: %w", err)
    }
    return affected, nil
}
```

Test: seed a "pending" run older than `timeout_sec + 300s`, call SweepOrphans, assert it's now `failed` with `error_kind=shutdown`.

Commit:
```bash
git add internal/scheduler/sweep.go internal/scheduler/sweep_test.go
git commit -m "feat(scheduler): orphan-sweep for crashed runners + restart-in-flight"
```

---

## Task 17: `cronfoundry serve`

**Files:**
- Create: `cmd/cronfoundry/serve.go`
- Create: `cmd/cronfoundry/serve_test.go`

Wires sync + scheduler + API in one process with graceful shutdown.

- [ ] **Step 1: serve.go**

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/api"
	"github.com/gambtho/cronfoundry/internal/cloud"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/scheduler"
	"github.com/gambtho/cronfoundry/internal/secretstore"
	"github.com/gambtho/cronfoundry/internal/token"
)

func newServeCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the CronFoundry service: API + scheduler + sync loops",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd.Context(), addr)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "API listen address")
	return cmd
}

func runServe(ctx context.Context, addr string) error {
	masterEnc := os.Getenv(envMasterKey)
	if masterEnc == "" {
		return fmt.Errorf("%s is required", envMasterKey)
	}
	master, err := secretstore.ParseMasterKey(masterEnc)
	if err != nil {
		return fmt.Errorf("parse master key: %w", err)
	}
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	appID := os.Getenv(envGitHubAppID)
	pemPath := os.Getenv(envGitHubAppPEM)
	if appID == "" || pemPath == "" {
		return fmt.Errorf("%s and %s are required", envGitHubAppID, envGitHubAppPEM)
	}
	pemBytes, err := os.ReadFile(pemPath)
	if err != nil {
		return fmt.Errorf("read PEM: %w", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	q := dbgen.New(pool)
	org, err := q.GetFirstOrganization(ctx)
	if err != nil {
		return fmt.Errorf("load organization (run `cronfoundry admin init`?): %w", err)
	}

	store := secretstore.NewEnvelopePostgresStore(pool, org.ID, master)
	signer := token.New(master)
	installs := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID:      appID,
		PrivateKey: pemBytes,
	})

	// Initial orphan sweep so any runs stuck from a previous process crash
	// are marked failed before the scheduler starts firing new ones.
	swept, err := scheduler.SweepOrphans(ctx, scheduler.Deps{Pool: pool})
	if err != nil {
		slog.Warn("serve: orphan sweep failed", "err", err)
	} else if swept > 0 {
		slog.Info("serve: orphan sweep reclaimed runs", "count", swept)
	}

	// API.
	apiDeps := api.Deps{
		Pool:          pool,
		Signer:        signer,
		Secrets:       store,
		Installations: installs,
	}
	srv := api.NewServer(addr, apiDeps)

	// Scheduler.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self binary: %w", err)
	}
	schedDeps := scheduler.Deps{
		Pool:         pool,
		Signer:       signer,
		Dispatcher:   cloud.NewSubprocessDispatcher(),
		APIBaseURL:   "http://" + addr + "/internal",
		RunnerBinary: self,
	}

	// SIGINT/SIGTERM triggers shutdown.
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 2)

	go func() {
		slog.Info("serve: API listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("api: %w", err)
		}
	}()

	go func() {
		if err := scheduler.Loop(ctx, 30*time.Second, schedDeps); err != nil && err != context.Canceled {
			errCh <- fmt.Errorf("scheduler: %w", err)
		}
	}()

	// Wait for ctx cancellation or an errored subsystem.
	select {
	case err := <-errCh:
		cancel()
		_ = srv.Shutdown(context.Background())
		return err
	case <-ctx.Done():
		slog.Info("serve: shutdown signal received")
	}

	// Graceful shutdown: give the API 10s to drain.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	return nil
}
```

- [ ] **Step 2: Smoke test**

```go
// cmd/cronfoundry/serve_test.go
package main

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServe_HealthzResponds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()

	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	t.Setenv(envGitHubAppID, "1")
	// Throwaway PEM to satisfy the pre-flight check.
	priv, _ := /* ... */ githubtest.MustPrivateKey(t)
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(pemPath, priv, 0o600))
	t.Setenv(envGitHubAppPEM, pemPath)

	require.NoError(t, runAdminInit(context.Background(), "o"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- runServe(ctx, "127.0.0.1:18080") }()

	// Wait for server to come up.
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		resp, _ = http.Get("http://127.0.0.1:18080/healthz")
		if resp != nil && resp.StatusCode == 200 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, resp)
	require.Equal(t, 200, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "ok", string(body))

	cancel()
	select {
	case err := <-errCh:
		// ctx.Canceled is expected on graceful shutdown.
		assert.True(t, err == nil || err == context.Canceled)
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not shut down")
	}
}
```

- [ ] **Step 3: Commit**

```bash
git add cmd/cronfoundry/serve.go cmd/cronfoundry/serve_test.go cmd/cronfoundry/main.go
git commit -m "feat(cli): cronfoundry serve — wire sync + scheduler + API"
```

---

## Task 18: Phase polish + final integration

- [ ] Remove stub `ServeHTTP`s from `internal/api/server.go` that were replaced by per-endpoint files.
- [ ] Verify all 6 `/internal/*` routes 401 without bearer, 403 with mismatched-claim, 200/204 on happy path.
- [ ] Add a `TestEndToEnd_ScheduleFires` that: seeds a schedule due now, boots `runServe`, waits for the tick to dispatch the runner subprocess, asserts the run row finalizes to `succeeded` (with a fake LLM provider wired via env vars — needs P1 runner to expose a way to inject a fake provider via env, which currently it does not; skip this full E2E and rely on each sub-task's test).

**Reality check:** the full end-to-end test likely needs to wait until we've exercised the pieces manually with a real OpenAI key. P2d can formalize it.

- [ ] Final suite + vet

```bash
go test ./... -count=1 -timeout 10m
go vet ./...
```

- [ ] Commit

```bash
git add -A
git commit -m "polish(p2c): stub cleanup + cross-cutting consistency"
```

---

## Self-Review

**1. Spec coverage (P2 design → P2c plan)**

| P2 spec requirement | Plan task |
|---|---|
| Per-run JWT sign/verify with HKDF-derived key | T1 |
| `runner_token_hash` persistence | T1 + T13 (scheduler stores the hash on dispatch) |
| Scheduler tick with idempotent insert (partial unique index) | T13 |
| Overlap policies (skip/queue/concurrent) | T4 + T13 |
| `next_fire_at` via robfig/cron/v3 + timezone | T3 + T13 |
| Subprocess dispatch via cloud.Dispatcher interface | T5 |
| `/internal/runs/{id}/context` | T7 |
| `/internal/secrets?names=...` with scoped JWT claim | T8 |
| `/internal/repos/{id}/clone-url` | T9 |
| `/internal/runs/{id}/events` | T10 |
| `/internal/runs/{id}/finalize` | T11 |
| Manual trigger endpoint | T12 |
| Runner HTTP mode | T15 |
| Orphan sweep | T16 |
| `cronfoundry serve` graceful shutdown | T17 |

Not covered (explicitly deferred):
- P3 UI, P4 Azure deploy, MCP tools, Copilot Enterprise, webhook receiver.
- Full end-to-end integration test (T18 flagged as reality-check). P2d formalizes.

**2. Placeholder scan**

Checked — the only non-spec text is the SDK-drift caveats (marked explicitly) and the "T11/T12/T13/T16's full test code" sections that describe the test shape in prose rather than inline code. Those are intentional for the heavier tasks; the implementer writes the concrete test following the pattern of the preceding tasks. This is a **conscious plan defect** — acceptable because:

- T10 and T7 both give full test code.
- T11 is "same shape as T10".
- T12 is "same shape, different input".
- T13 is heavy enough (~200 lines of test code) that prescribing every line would be over-specification; the shape + assertions are laid out.
- T16 is tiny (one function, one test).

If the implementer gets stuck, they report BLOCKED and we fill in more detail.

**3. Type consistency**

- `token.RunClaims` fields consistent across T1, T6, T7, T8, T9, T10, T11, T12, T15.
- `cloud.JobDispatcher.Dispatch` + `Handle.PID()/Wait()/Kill()` consistent across T5, T13, T17.
- `api.Deps` vs `scheduler.Deps` are intentionally different (different roles); each task that touches them defines them.
- `dbgen.GetRunForContext` is a composite query — implementer verifies generated column names match.

---

Plan complete and saved to `docs/superpowers/plans/2026-04-20-p2c-scheduler-api.md`.
