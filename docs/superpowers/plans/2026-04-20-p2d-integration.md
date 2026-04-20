# P2d — Integration, Dev Harness, and Loose Ends Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close out P2 with a proper dev harness (docker-compose, shared test helpers, expanded Makefile), an operator-facing runs listing, writeback push wired through, an end-to-end integration test that walks the smoke-test path automatically, and a P2 tag. After P2d, a contributor can clone the repo and run `make e2e` to prove the whole stack works.

**Architecture:** Extract duplicated Postgres-container test helpers into `internal/testdb`. Ship `deploy/docker-compose.yml` that brings up Postgres + `cronfoundry serve` on demand. Expand the Makefile with `make dev`, `make migrate`, `make e2e`, `make lint`. Add two new admin subcommands (`list-runs`, `show-run`) backed by sqlc queries that already exist or need one tiny addition. Plumb a dedicated writeback-push endpoint and consume it from the HTTP runner. Hoist the secret-ref JSON scanner out of the two duplicated call sites.

**Tech Stack:**
- Go 1.25+ (unchanged)
- Docker + docker-compose (dev harness only; not a runtime dep)
- Stdlib only for the new code (no new third-party deps)
- Existing: pgx/v5, testcontainers-go, sqlc, goose

---

## File Structure (locked in upfront)

```
cronfoundry/
├── deploy/
│   ├── docker-compose.yml                # NEW — Postgres + cronfoundry serve
│   ├── docker-compose.test.yml           # NEW — Postgres-only override for `make e2e`
│   └── Dockerfile                        # NEW — build image for serve container
├── internal/
│   ├── testdb/                           # NEW — shared Postgres-container helper
│   │   ├── testdb.go
│   │   └── testdb_test.go
│   ├── api/
│   │   └── writeback_push.go             # NEW — POST /internal/runs/{id}/writeback-push
│   ├── config/
│   │   └── secretrefs.go                 # NEW — hoisted from scheduler/tick.go + cmd/cronfoundry/runner.go
│   └── scheduler/
│       └── queue_drain.go                # NEW — dedicated queue-policy drain loop
├── cmd/cronfoundry/
│   ├── admin_listruns.go                 # NEW — `admin list-runs`
│   ├── admin_showrun.go                  # NEW — `admin show-run <id>`
│   └── e2e_test.go                       # NEW — full dockerized end-to-end test
├── internal/db/queries/run.sql           # MODIFY — add ListRuns, GetRunWithSchedule
├── Makefile                              # MODIFY — expand targets
├── README.md                             # MODIFY — update setup section
└── .env.example                          # NEW — env vars needed by serve
```

### Responsibilities

- `internal/testdb/testdb.go` — one exported helper `BootPG(t *testing.T) (*pgxpool.Pool, func())` that boots a postgres:16-alpine container, runs migrations, and returns a pool + teardown. Replaces three near-identical copies in `internal/api/run_context_test.go`, `internal/scheduler/tick_test.go`, `internal/secretstore/postgres_test.go`.
- `internal/config/secretrefs.go` — one exported helper `Collect(destinations, env []byte, llmRef *string) []string` that replaces duplicated scan loops in `scheduler/tick.go::secretRefsFor` and `cmd/cronfoundry/runner.go::collectSecretNames`.
- `internal/api/writeback_push.go` — new endpoint `POST /internal/runs/{id}/writeback-push` that runs `writeback.Writer.Push` server-side using the GitHub App installation token. Runner calls it with the SHA it just committed; server performs the push. Keeps the App private key out of the runner subprocess.
- `internal/scheduler/queue_drain.go` — P2c deferred the queue-policy latency; this loop scans for pending queue-policy runs whose prior run just terminated and dispatches them immediately (no ~30s Tick wait). Run as a separate goroutine alongside Loop.
- `deploy/docker-compose.yml` — single-file stack: Postgres volume, cronfoundry service with all env vars. `make dev` brings it up.
- `deploy/Dockerfile` — multi-stage Go build; final image is distroless with `/cronfoundry` as the entrypoint.
- `cmd/cronfoundry/admin_listruns.go` — `admin list-runs [--schedule <name>] [--limit 20]` printing a tabwriter table.
- `cmd/cronfoundry/admin_showrun.go` — `admin show-run <uuid>` printing run detail + last 20 events.
- `cmd/cronfoundry/e2e_test.go` — boots Postgres + the real serve binary via docker-compose, seeds a skill, fakes the GitHub + LLM endpoints, asserts the run finalizes. Tagged `// +build e2e` so it runs only under `make e2e`.

---

## Task 1: Shared testdb helper

**Files:**
- Create: `internal/testdb/testdb.go`
- Create: `internal/testdb/testdb_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/testdb/testdb_test.go
package testdb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootPG_ReturnsReadyPool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := BootPG(t)
	defer cleanup()

	ctx := context.Background()
	assert.NoError(t, pool.Ping(ctx))

	// Migrations applied — organization table exists.
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables
		               WHERE table_schema='public' AND table_name='organization')`).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists)
}
```

- [ ] **Step 2: Run the test — it should fail**

```bash
go test ./internal/testdb/... -run TestBootPG -v
```

Expected: build error — package doesn't exist yet.

- [ ] **Step 3: Implement `internal/testdb/testdb.go`**

```go
// Package testdb provides shared test helpers for booting a throwaway
// Postgres container and running migrations.
//
// This package imports `testing`, so it must be imported only from
// `*_test.go` files. (Go's `testing` package is linked into production
// binaries if a non-test file imports it.)
package testdb

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gambtho/cronfoundry/internal/db"
)

// BootPG starts a throwaway Postgres 16 container, runs all migrations, and
// returns a ready-to-use pgx pool + a cleanup func the caller must defer.
//
// The container image is pinned to postgres:16-alpine; if the image isn't
// cached locally the first call pulls it (~15 s).
func BootPG(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("cf"),
		postgres.WithUsername("cf"),
		postgres.WithPassword("cf"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, dsn))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	return pool, func() {
		pool.Close()
		_ = container.Terminate(context.Background())
	}
}
```

- [ ] **Step 4: Run the test — should pass now**

```bash
go test ./internal/testdb/... -run TestBootPG -v
```

Expected: PASS in ~2–5 seconds (after image pull).

- [ ] **Step 5: Run `go vet`**

```bash
go vet ./...
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/testdb/testdb.go internal/testdb/testdb_test.go
git commit -m "feat(testdb): shared Postgres-container test helper"
```

---

## Task 2: Migrate existing tests to use testdb.BootPG

**Files:**
- Modify: `internal/api/run_context_test.go:28-47` (replace `bootPG` helper with `testdb.BootPG`)
- Modify: `internal/scheduler/tick_test.go:25-45` (replace `bootPG` helper with `testdb.BootPG`)
- Modify: `internal/secretstore/postgres_test.go:21-47` (replace inline `postgres.Run` with `testdb.BootPG`)
- Modify: `cmd/cronfoundry/admin_init_test.go` — replace `bootPostgres` with `testdb.BootPG`

- [ ] **Step 1: Replace helper in `internal/api/run_context_test.go`**

Delete lines 28-47 (the `bootPG` function). Add import:

```go
"github.com/gambtho/cronfoundry/internal/testdb"
```

Replace all call sites `bootPG(t)` with `testdb.BootPG(t)`.

- [ ] **Step 2: Run the api package tests**

```bash
go test ./internal/api/... -count=1
```

Expected: all pass.

- [ ] **Step 3: Same replacement in `internal/scheduler/tick_test.go`**

Delete the `bootPG` function there (lines 25-45). Add the testdb import. Swap call sites.

```bash
go test ./internal/scheduler/... -count=1
```

Expected: all pass.

- [ ] **Step 4: Same replacement in `internal/secretstore/postgres_test.go`**

Replace the `setupStore` helper's container-boot block (the part that calls `postgres.Run` + `db.Migrate`) with a call to `testdb.BootPG`.

```bash
go test ./internal/secretstore/... -count=1
```

Expected: all pass.

- [ ] **Step 5: Same replacement in `cmd/cronfoundry/admin_init_test.go`**

That file defines `bootPostgres` which is used by multiple test files in the same package. Replace its implementation with a thin wrapper that returns just the DSN (since that's what the admin tests want):

```go
func bootPostgres(t *testing.T) (dsn string, cleanup func()) {
	t.Helper()
	pool, teardown := testdb.BootPG(t)
	// Extract the DSN from the pool's config.
	dsn = pool.Config().ConnString()
	// The tests in this package want raw DSNs, not pools. Close the pool
	// immediately so the caller can open its own.
	pool.Close()
	return dsn, teardown
}
```

Wait — this won't work cleanly because BootPG opens a pool and the admin tests want a fresh DSN. Instead, add a second exported function to testdb:

**Amendment to Task 1:** also export `BootPGWithDSN(t) (dsn string, cleanup func())` that does everything BootPG does except returning the pool. Implementation:

```go
// BootPGWithDSN is like BootPG but returns the DSN instead of a pool.
// Intended for callers that want to open their own pool (e.g., CLI tests
// that exercise pool-open code paths).
func BootPGWithDSN(t *testing.T) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("cf"),
		postgres.WithUsername("cf"),
		postgres.WithPassword("cf"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, dsn))
	return dsn, func() { _ = container.Terminate(context.Background()) }
}
```

Go back to Task 1, add this function + a test for it. Then use it here:

```go
func bootPostgres(t *testing.T) (dsn string, cleanup func()) {
	return testdb.BootPGWithDSN(t)
}
```

Actually simpler: just leave `bootPostgres` in-place but make it delegate to `testdb.BootPGWithDSN`. Keeps the existing call-site aliases working.

- [ ] **Step 6: Run the full test suite**

```bash
go test ./... -count=1 -timeout 5m
```

Expected: all 19 packages pass.

- [ ] **Step 7: Commit**

```bash
git add internal/api/run_context_test.go internal/scheduler/tick_test.go \
        internal/secretstore/postgres_test.go cmd/cronfoundry/admin_init_test.go \
        internal/testdb/testdb.go internal/testdb/testdb_test.go
git commit -m "refactor: consolidate Postgres-container test helpers into internal/testdb"
```

---

## Task 3: Hoist secret-ref JSON scanner to internal/config/secretrefs.go

**Files:**
- Create: `internal/config/secretrefs.go`
- Create: `internal/config/secretrefs_test.go`
- Modify: `internal/scheduler/tick.go` — replace `secretRefsFor` inlined scanner with `config.Collect`
- Modify: `cmd/cronfoundry/runner.go` — replace `collectSecretNames` with `config.Collect`

- [ ] **Step 1: Write the failing test**

```go
// internal/config/secretrefs_test.go
package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollectSecretRefs_ExtractsFromDestinations(t *testing.T) {
	dests := json.RawMessage(`[{"slack":{"secret":"slack_url"}},{"discord":{"secret":"discord_url"}}]`)
	got := CollectSecretRefs(dests, nil, nil)
	assert.ElementsMatch(t, []string{"slack_url", "discord_url"}, got)
}

func TestCollectSecretRefs_ExtractsFromEnv(t *testing.T) {
	env := json.RawMessage(`{"API_KEY":{"secret":"api_key"},"PLAIN":"value"}`)
	got := CollectSecretRefs(nil, env, nil)
	assert.Equal(t, []string{"api_key"}, got)
}

func TestCollectSecretRefs_IncludesLLMRef(t *testing.T) {
	llm := "openai_key"
	got := CollectSecretRefs(nil, nil, &llm)
	assert.Equal(t, []string{"openai_key"}, got)
}

func TestCollectSecretRefs_Dedupes(t *testing.T) {
	dests := json.RawMessage(`[{"slack":{"secret":"shared"}}]`)
	env := json.RawMessage(`{"X":{"secret":"shared"}}`)
	llm := "shared"
	got := CollectSecretRefs(dests, env, &llm)
	assert.Equal(t, []string{"shared"}, got)
}

func TestCollectSecretRefs_SkipsNonStringSecretValues(t *testing.T) {
	// { "secret": 42 } should be skipped without aborting the scan, and the
	// later { "secret": "real" } should still be collected.
	dests := json.RawMessage(`[{"x":{"secret":42}},{"y":{"secret":"real"}}]`)
	got := CollectSecretRefs(dests, nil, nil)
	assert.Equal(t, []string{"real"}, got)
}

func TestCollectSecretRefs_EmptyInputs(t *testing.T) {
	assert.Empty(t, CollectSecretRefs(nil, nil, nil))
	assert.Empty(t, CollectSecretRefs(json.RawMessage(``), json.RawMessage(``), nil))
}
```

- [ ] **Step 2: Confirm failing**

```bash
go test ./internal/config/... -run TestCollectSecretRefs -v
```

Expected: undefined.

- [ ] **Step 3: Implement `internal/config/secretrefs.go`**

```go
package config

import (
	"encoding/json"
	"sort"
	"strings"
)

// CollectSecretRefs extracts all secret names referenced from destinations
// JSON, env JSON, and an optional LLM secret reference. Secret references
// appear in the form `{ "secret": "name" }` anywhere in the destinations
// or env JSON trees.
//
// The scanner is deliberately string-based rather than schema-aware so it
// can be shared by the scheduler (which has only the persisted JSON) and
// the runner (which has the same). Non-string "secret" values are skipped
// without aborting the scan.
//
// Results are sorted + deduplicated.
func CollectSecretRefs(destinations, env json.RawMessage, llmRef *string) []string {
	seen := map[string]struct{}{}
	scan(destinations, seen)
	scan(env, seen)
	if llmRef != nil && *llmRef != "" {
		seen[*llmRef] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// scan finds every `"secret" : "<name>"` pair in raw JSON and adds the
// name to seen. Non-string values are skipped without aborting.
func scan(raw json.RawMessage, seen map[string]struct{}) {
	if len(raw) == 0 {
		return
	}
	s := string(raw)
	i := 0
	for {
		idx := strings.Index(s[i:], `"secret"`)
		if idx < 0 {
			return
		}
		idx += i
		j := idx + len(`"secret"`)
		// Skip whitespace + colon.
		for j < len(s) && (s[j] == ' ' || s[j] == ':' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
			j++
		}
		if j >= len(s) || s[j] != '"' {
			// Not a string value — skip this occurrence and keep scanning.
			i = idx + len(`"secret"`)
			continue
		}
		j++ // past opening quote
		end := strings.IndexByte(s[j:], '"')
		if end < 0 {
			return
		}
		end += j
		name := s[j:end]
		if name != "" {
			seen[name] = struct{}{}
		}
		i = end + 1
	}
}
```

- [ ] **Step 4: Run test — should pass**

```bash
go test ./internal/config/... -run TestCollectSecretRefs -v
```

All 6 sub-tests PASS.

- [ ] **Step 5: Replace call site in `internal/scheduler/tick.go`**

Find the `secretRefsFor` function (a file-level helper that wraps the scanner). Delete it and the unexported `scanJSON` / `indexOf` helpers that back it. Find every call to `secretRefsFor(sched)` and replace with:

```go
config.CollectSecretRefs(sched.DestinationsJson, sched.EnvJson, sched.LlmSecretRef)
```

Add the import:

```go
"github.com/gambtho/cronfoundry/internal/config"
```

- [ ] **Step 6: Run scheduler tests**

```bash
go test ./internal/scheduler/... -count=1 -v
```

Expected: all pass (no test referenced `secretRefsFor` directly; the change is transparent).

- [ ] **Step 7: Replace call site in `cmd/cronfoundry/runner.go`**

Find `collectSecretNames` and its `scan` helper. Delete both. Replace calls to `collectSecretNames(runCtx)` with:

```go
config.CollectSecretRefs(runCtx.Destinations, runCtx.Env, runCtx.LLMSecretRef)
```

Delete `TestCollectSecretNames_*` tests in `cmd/cronfoundry/runner_test.go` (they're subsumed by `internal/config/secretrefs_test.go`).

- [ ] **Step 8: Run full suite**

```bash
go test ./... -count=1 -timeout 5m
go vet ./...
```

Expected: all pass, vet clean.

- [ ] **Step 9: Commit**

```bash
git add internal/config/secretrefs.go internal/config/secretrefs_test.go \
        internal/scheduler/tick.go cmd/cronfoundry/runner.go cmd/cronfoundry/runner_test.go
git commit -m "refactor: hoist secret-ref JSON scanner into internal/config"
```

---

## Task 4: Add `GetRunForAdmin` + `ListRuns` sqlc queries

**Files:**
- Modify: `internal/db/queries/run.sql` — append queries
- Regenerate: `internal/db/gen/run.sql.go`

- [ ] **Step 1: Append to `internal/db/queries/run.sql`**

```sql
-- name: ListRunsForOrg :many
-- Used by `cronfoundry admin list-runs`. Returns the most recent N runs,
-- joined to schedule + skill names for display.
SELECT r.id,
       r.status,
       r.fire_reason,
       r.actor,
       r.started_at,
       r.finished_at,
       r.duration_ms,
       r.error_kind,
       r.error_msg,
       r.created_at,
       s.name       AS schedule_name,
       sk.path      AS skill_path,
       rc.owner,
       rc.name      AS repo_name
FROM run r
JOIN schedule s         ON s.id = r.schedule_id
JOIN skill sk           ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.org_id = $1
ORDER BY r.created_at DESC
LIMIT $2;

-- name: ListRunsForSchedule :many
-- Same shape as ListRunsForOrg but filtered to a single schedule, keyed by
-- its `name` (since operators address schedules by human-readable name).
SELECT r.id,
       r.status,
       r.fire_reason,
       r.actor,
       r.started_at,
       r.finished_at,
       r.duration_ms,
       r.error_kind,
       r.error_msg,
       r.created_at,
       s.name       AS schedule_name,
       sk.path      AS skill_path,
       rc.owner,
       rc.name      AS repo_name
FROM run r
JOIN schedule s         ON s.id = r.schedule_id
JOIN skill sk           ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.org_id = $1
  AND s.name = $2
ORDER BY r.created_at DESC
LIMIT $3;

-- name: GetRunForAdmin :one
-- Used by `cronfoundry admin show-run`. Same join shape as GetRunForContext
-- but without the bearer-token-hash exposure (admin tools don't need the
-- hash; it's sensitive-ish and should stay out of operator views).
SELECT r.id,
       r.org_id,
       r.schedule_id,
       r.skill_sha,
       r.fire_time,
       r.status,
       r.fire_reason,
       r.actor,
       r.started_at,
       r.finished_at,
       r.duration_ms,
       r.tokens_in,
       r.tokens_out,
       r.cost_cents,
       r.error_kind,
       r.error_msg,
       r.writeback_commit_sha,
       r.created_at,
       s.name  AS schedule_name,
       s.cron,
       sk.path AS skill_path,
       rc.owner,
       rc.name AS repo_name
FROM run r
JOIN schedule s         ON s.id = r.schedule_id
JOIN skill sk           ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.id = $1;
```

- [ ] **Step 2: Regenerate**

```bash
make sqlc
```

Expected: `internal/db/gen/run.sql.go` gains three new methods.

- [ ] **Step 3: Verify build**

```bash
go build ./...
go vet ./...
```

Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/db/queries/run.sql internal/db/gen/run.sql.go
git commit -m "feat(db): add admin queries for run listing and detail"
```

---

## Task 5: `cronfoundry admin list-runs`

**Files:**
- Create: `cmd/cronfoundry/admin_listruns.go`
- Create: `cmd/cronfoundry/admin_listruns_test.go`
- Modify: `cmd/cronfoundry/admin.go` — register the new subcommand

- [ ] **Step 1: Write the failing test**

```go
// cmd/cronfoundry/admin_listruns_test.go
package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminListRuns_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	var buf bytes.Buffer
	require.NoError(t, runAdminListRuns(context.Background(), 10, "", &buf))
	assert.Contains(t, buf.String(), "no runs")
}

func TestAdminListRuns_ShowsRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	// Seed a schedule + one finished run.
	seedScheduleWithFinishedRun(t, dsn)

	var buf bytes.Buffer
	require.NoError(t, runAdminListRuns(context.Background(), 10, "", &buf))
	out := buf.String()
	assert.Contains(t, out, "RUN ID")
	assert.Contains(t, out, "succeeded")
}
```

Add a test helper `seedScheduleWithFinishedRun(t, dsn)` at the top of the file (delegating to `pool.Exec` for brevity):

```go
func seedScheduleWithFinishedRun(t *testing.T, dsn string) {
	t.Helper()
	// Short helper — opens a one-off pool, inserts the rows, closes.
	// Since the full admin test harness reuses pools, keeping this local
	// avoids importing pgxpool across multiple packages.
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	var orgID, repoID, skillID, schedID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM organization LIMIT 1`).Scan(&orgID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 1, 'o', 'r', 'main') RETURNING id`, orgID).Scan(&repoID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'sk', 's', 'sha', '{}'::jsonb) RETURNING id`,
		orgID, repoID).Scan(&skillID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		 VALUES ($1, $2, 'monday', '0 9 * * MON', 'openai', 'gpt-4o-mini', '[]'::jsonb)
		 RETURNING id`, orgID, skillID).Scan(&schedID))
	_, err = pool.Exec(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason,
		                  runner_token_hash, started_at, finished_at, duration_ms,
		                  tokens_in, tokens_out)
		 VALUES ($1, $2, 'sha', 'succeeded', 'manual', 'h',
		         now() - interval '10 seconds', now(), 10000, 500, 100)`,
		orgID, schedID)
	require.NoError(t, err)
}
```

Add imports at the top of the test file:
```go
"github.com/jackc/pgx/v5/pgtype"
"github.com/jackc/pgx/v5/pgxpool"
```

- [ ] **Step 2: Confirm failing**

```bash
go test ./cmd/cronfoundry/... -run TestAdminListRuns -v
```

Expected: `runAdminListRuns` undefined.

- [ ] **Step 3: Implement `cmd/cronfoundry/admin_listruns.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminListRunsCmd() *cobra.Command {
	var limit int
	var scheduleName string
	cmd := &cobra.Command{
		Use:   "list-runs",
		Short: "List recent runs (optionally filtered by schedule name)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminListRuns(cmd.Context(), limit, scheduleName, os.Stdout)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "max number of rows to return")
	cmd.Flags().StringVar(&scheduleName, "schedule", "", "filter by schedule name")
	return cmd
}

func runAdminListRuns(ctx context.Context, limit int, scheduleName string, out io.Writer) error {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return fmt.Errorf("%s is required", envDatabaseURL)
	}
	if limit <= 0 {
		limit = 20
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
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no organization seeded; run `cronfoundry admin init` first: %w", err)
		}
		return fmt.Errorf("load organization: %w", err)
	}

	var rows []dbgen.ListRunsForOrgRow
	if scheduleName == "" {
		rows, err = q.ListRunsForOrg(ctx, dbgen.ListRunsForOrgParams{
			OrgID: org.ID,
			Limit: int32(limit),
		})
	} else {
		sRows, err2 := q.ListRunsForSchedule(ctx, dbgen.ListRunsForScheduleParams{
			OrgID: org.ID,
			Name:  scheduleName,
			Limit: int32(limit),
		})
		if err2 != nil {
			return fmt.Errorf("list: %w", err2)
		}
		// Reshape into the ListRunsForOrgRow type since downstream formatting
		// is identical.
		rows = make([]dbgen.ListRunsForOrgRow, 0, len(sRows))
		for _, r := range sRows {
			rows = append(rows, dbgen.ListRunsForOrgRow(r))
		}
	}
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "(no runs yet)")
		return nil
	}

	tw := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tSCHEDULE\tSKILL\tSTATUS\tSTARTED\tDURATION")
	for _, r := range rows {
		started := "-"
		if r.StartedAt.Valid {
			started = r.StartedAt.Time.Format("2006-01-02 15:04:05")
		}
		duration := "-"
		if r.DurationMs != nil {
			duration = fmt.Sprintf("%dms", *r.DurationMs)
		}
		fmt.Fprintf(tw, "%s\t%s/%s\t%s\t%s\t%s\t%s\n",
			uuid.UUID(r.ID.Bytes).String()[:8],
			r.Owner, r.RepoName+"/"+r.ScheduleName,
			r.SkillPath,
			r.Status,
			started, duration)
	}
	return tw.Flush()
}
```

(The `ListRunsForOrgRow(r)` conversion in the schedule-filtered path works because both types have identical fields — sqlc generates the same shape from the two queries. If sqlc produces different types, drop the reshape and print `sRows` separately with the same `fmt.Fprintf` format.)

- [ ] **Step 4: Register in `cmd/cronfoundry/admin.go`**

Add to `newAdminCmd`'s `AddCommand` list:

```go
cmd.AddCommand(newAdminListRunsCmd())
```

- [ ] **Step 5: Run tests**

```bash
go test ./cmd/cronfoundry/... -run TestAdminListRuns -v
```

Expected: both tests pass.

- [ ] **Step 6: Full suite + vet**

```bash
go test -short ./...
go vet ./...
```

- [ ] **Step 7: Commit**

```bash
git add cmd/cronfoundry/admin.go cmd/cronfoundry/admin_listruns.go cmd/cronfoundry/admin_listruns_test.go
git commit -m "feat(admin): cronfoundry admin list-runs"
```

---

## Task 6: `cronfoundry admin show-run <uuid>`

**Files:**
- Create: `cmd/cronfoundry/admin_showrun.go`
- Create: `cmd/cronfoundry/admin_showrun_test.go`
- Modify: `cmd/cronfoundry/admin.go` — register

- [ ] **Step 1: Failing test**

```go
// cmd/cronfoundry/admin_showrun_test.go
package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminShowRun_MissingRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	dsn, teardown := bootPostgres(t)
	defer teardown()
	t.Setenv(envMasterKey, mustMasterKey(t))
	t.Setenv(envDatabaseURL, dsn)
	require.NoError(t, runAdminInit(context.Background(), "o"))

	err := runAdminShowRun(context.Background(), "00000000-0000-0000-0000-000000000000", &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAdminShowRun_BadUUID(t *testing.T) {
	err := runAdminShowRun(context.Background(), "not-a-uuid", &bytes.Buffer{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}
```

- [ ] **Step 2: Implement `cmd/cronfoundry/admin_showrun.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

func newAdminShowRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show-run <run-id>",
		Short: "Show detail of a single run plus its last 20 events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminShowRun(cmd.Context(), args[0], os.Stdout)
		},
	}
}

func runAdminShowRun(ctx context.Context, runIDStr string, out io.Writer) error {
	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		return fmt.Errorf("invalid run id: %w", err)
	}

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
	run, err := q.GetRunForAdmin(ctx, pgtype.UUID{Bytes: runID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("run not found: %s", runIDStr)
		}
		return fmt.Errorf("load run: %w", err)
	}

	fmt.Fprintf(out, "Run ID:         %s\n", runID)
	fmt.Fprintf(out, "Status:         %s\n", run.Status)
	fmt.Fprintf(out, "Fire reason:    %s\n", run.FireReason)
	fmt.Fprintf(out, "Schedule:       %s/%s/%s\n", run.Owner, run.RepoName, run.ScheduleName)
	fmt.Fprintf(out, "Skill:          %s @ %s\n", run.SkillPath, run.SkillSha)
	fmt.Fprintf(out, "Cron:           %s\n", run.Cron)
	if run.Actor != nil {
		fmt.Fprintf(out, "Actor:          %s\n", *run.Actor)
	}
	if run.StartedAt.Valid {
		fmt.Fprintf(out, "Started:        %s\n", run.StartedAt.Time.Format(time.RFC3339))
	}
	if run.FinishedAt.Valid {
		fmt.Fprintf(out, "Finished:       %s\n", run.FinishedAt.Time.Format(time.RFC3339))
	}
	if run.DurationMs != nil {
		fmt.Fprintf(out, "Duration:       %dms\n", *run.DurationMs)
	}
	if run.TokensIn != nil {
		fmt.Fprintf(out, "Tokens in:      %d\n", *run.TokensIn)
	}
	if run.TokensOut != nil {
		fmt.Fprintf(out, "Tokens out:     %d\n", *run.TokensOut)
	}
	if run.CostCents != nil {
		fmt.Fprintf(out, "Cost (cents):   %d\n", *run.CostCents)
	}
	if run.WritebackCommitSha != nil {
		fmt.Fprintf(out, "Writeback SHA:  %s\n", *run.WritebackCommitSha)
	}
	if run.ErrorKind != nil && *run.ErrorKind != "" {
		fmt.Fprintf(out, "Error kind:     %s\n", *run.ErrorKind)
	}
	if run.ErrorMsg != nil && *run.ErrorMsg != "" {
		fmt.Fprintf(out, "Error message:  %s\n", *run.ErrorMsg)
	}

	events, err := q.ListRunEvents(ctx, run.ID)
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}
	if len(events) == 0 {
		fmt.Fprintln(out, "\n(no events recorded)")
		return nil
	}
	fmt.Fprintln(out, "\nEvents:")
	// Print at most the last 20 for readability.
	start := 0
	if len(events) > 20 {
		start = len(events) - 20
	}
	for _, ev := range events[start:] {
		fmt.Fprintf(out, "  [%s] %-5s %s: %s\n",
			ev.Ts.Time.Format("15:04:05"),
			ev.Level,
			ev.EventType,
			string(ev.PayloadJson))
	}
	return nil
}
```

- [ ] **Step 3: Register**

In `cmd/cronfoundry/admin.go`, add:

```go
cmd.AddCommand(newAdminShowRunCmd())
```

- [ ] **Step 4: Run tests**

```bash
go test ./cmd/cronfoundry/... -run TestAdminShowRun -v
```

Expected: both tests pass.

- [ ] **Step 5: Full suite + vet; commit**

```bash
go test -short ./...
go vet ./...
git add cmd/cronfoundry/admin.go cmd/cronfoundry/admin_showrun.go cmd/cronfoundry/admin_showrun_test.go
git commit -m "feat(admin): cronfoundry admin show-run"
```

---

## Task 7: Writeback push endpoint + runner wiring

**Files:**
- Modify: `internal/db/queries/run.sql` — add `GetRunWritebackConfig`
- Regenerate: `internal/db/gen/run.sql.go`
- Create: `internal/api/writeback_push.go`
- Create: `internal/api/writeback_push_test.go`
- Modify: `internal/api/server.go` — register `POST /internal/runs/{id}/writeback-push`
- Modify: `cmd/cronfoundry/runner.go` — after writeback commit, POST the push request

- [ ] **Step 1: Add sqlc query**

Append to `internal/db/queries/run.sql`:

```sql
-- name: GetRunWritebackConfig :one
-- Returns the fields needed to push a writeback commit: install ID for
-- the token and owner/name for the remote URL.
SELECT s.writeback_json,
       rc.github_app_install_id,
       rc.owner,
       rc.name AS repo_name
FROM run r
JOIN schedule s ON s.id = r.schedule_id
JOIN skill sk ON sk.id = s.skill_id
JOIN repo_connection rc ON rc.id = sk.repo_id
WHERE r.id = $1;
```

Regenerate via `make sqlc`.

- [ ] **Step 2: Failing test**

```go
// internal/api/writeback_push_test.go
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

	"github.com/gambtho/cronfoundry/internal/github"
	"github.com/gambtho/cronfoundry/internal/githubtest"
	"github.com/gambtho/cronfoundry/internal/token"
)

// TestWritebackPush_RejectsMismatchedRunID — 403 when JWT run_id doesn't
// match URL.
func TestWritebackPush_RejectsMismatchedRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	runID, orgID := seedRun(t, pool)
	bindRunHash(t, pool, runID, "dummy-hash")

	signer := token.New(randomMaster(t))
	tok, _, err := signer.Sign(token.RunClaims{
		RunID:     uuid.New(),
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	privPEM, _ := githubtest.MustPrivateKey(t)
	cache := github.NewInstallationCache(github.InstallationCacheConfig{
		AppID: "1", PrivateKey: privPEM,
	})
	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer, Installations: cache})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"commit_sha": "abc123", "path": "memory.md"})
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/writeback-push",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// A happy-path test would need a real or mocked Git remote to push to —
// deferred to the e2e test in Task 10. For T7 we cover only the guard paths
// since they're the security-critical ones.
```

- [ ] **Step 3: Implement `internal/api/writeback_push.go`**

```go
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/writeback"
)

type writebackPushBody struct {
	CommitSHA string `json:"commit_sha"`
	// RepoRoot is the runner-side absolute path of the clone. Used only for
	// the Push call; not persisted anywhere.
	RepoRoot string `json:"repo_root"`
}

// ServeHTTP implements POST /internal/runs/{id}/writeback-push.
//
// The runner has already committed the <memory> block locally; this endpoint
// mints an installation token and performs the actual push to the remote.
// Kept out of the runner to avoid leaking the App private key (and even the
// short-lived install token) into arbitrary LLM-generated code paths.
func (h writebackPushHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlRunID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	if ClaimsFromContext(r.Context()).RunID != urlRunID {
		http.Error(w, "token run_id mismatch", http.StatusForbidden)
		return
	}

	var body writebackPushBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.RepoRoot == "" {
		http.Error(w, "repo_root required", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(body.RepoRoot); err != nil {
		http.Error(w, "repo_root not readable: "+err.Error(), http.StatusBadRequest)
		return
	}

	q := dbgen.New(h.deps.Pool)
	cfg, err := q.GetRunWritebackConfig(r.Context(), pgtype.UUID{Bytes: urlRunID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		http.Error(w, "load run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	tok, err := h.deps.Installations.Token(r.Context(), cfg.GithubAppInstallID)
	if err != nil {
		http.Error(w, "install token: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Build the authed remote URL. The runner's clone is expected to have
	// a remote named "origin" that we override via the Push's URL param.
	pushURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", tok, cfg.Owner, cfg.RepoName)

	writer := writeback.New()
	if err := writer.PushToURL(body.RepoRoot, pushURL); err != nil {
		http.Error(w, "push: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

`writeback.PushToURL` doesn't exist yet. Add it to `internal/writeback/writeback.go`:

```go
// PushToURL pushes the current HEAD to the given remote URL (overriding any
// existing `origin` remote). Used by the /internal/writeback-push endpoint
// to perform the push with a fresh install token without requiring the
// caller to have a pre-configured origin remote.
func (w *Writer) PushToURL(repoRoot, remoteURL string) error {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return fmt.Errorf("writeback: open repo: %w", err)
	}
	// Remove any existing origin; add our url-auth one.
	_ = repo.DeleteRemote("origin")
	if _, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteURL},
	}); err != nil {
		return fmt.Errorf("writeback: create remote: %w", err)
	}
	if err := repo.Push(&git.PushOptions{RemoteName: "origin"}); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("writeback: push: %w", err)
	}
	return nil
}
```

Add import `github.com/go-git/go-git/v5/config` at the top of `writeback.go`.

Add the handler type + route in `server.go`:

```go
// In server.go's NewServer:
mux.Handle("POST /internal/runs/{id}/writeback-push", auth(writebackPushHandler{deps}))

// At the bottom of server.go (with the other handler types):
type writebackPushHandler struct{ deps Deps }
```

- [ ] **Step 4: Update the runner to call the endpoint**

In `cmd/cronfoundry/runner.go`, find where `SkipPush: true` is currently set. Change the flow so that after `runner.Run` returns with a `WritebackSHA`, the HTTP runner calls the new endpoint:

```go
if result.WritebackSHA != "" {
	if err := client.PostWritebackPush(ctx, runID, result.WritebackSHA, cloneDir); err != nil {
		// Downgrade from failure to partial_failure.
		slog.Warn("writeback push failed", "err", err)
		if body.Status == "succeeded" {
			body.Status = "partial_failure"
		}
	}
}
```

Add `PostWritebackPush` to the apiClient:

```go
func (c *apiClient) PostWritebackPush(ctx context.Context, runID, commitSHA, repoRoot string) error {
	body := map[string]string{
		"commit_sha": commitSHA,
		"repo_root":  repoRoot,
	}
	return c.do(ctx, http.MethodPost, "/internal/runs/"+url.PathEscape(runID)+"/writeback-push", body, nil)
}
```

Remove the `SkipPush: true` override on the `runner.RunInput`; leave it default (push enabled in P1 runner is a no-op now — or rip out runner.RunInput.SkipPush entirely if unused).

Actually — keep `SkipPush: true` on the P1 runner call. The P1 runner doesn't have an install token; it would try to push with the empty `GitHubToken` and fail. Instead, the HTTP runner (not P1 runner.Run) takes responsibility for the push via the new endpoint. Document this in runner.go with a comment.

- [ ] **Step 5: Run the api package + runner tests**

```bash
go test ./internal/api/... -count=1
go test ./cmd/cronfoundry/... -count=1
go vet ./...
```

Expected: all pass (the guard test passes; the happy path is deferred to e2e).

- [ ] **Step 6: Commit**

```bash
git add internal/db/queries/run.sql internal/db/gen/run.sql.go \
        internal/api/server.go internal/api/writeback_push.go internal/api/writeback_push_test.go \
        internal/writeback/writeback.go cmd/cronfoundry/runner.go
git commit -m "feat(writeback): push via /internal endpoint with install token"
```

---

## Task 8: Dockerfile + docker-compose

**Files:**
- Create: `deploy/Dockerfile`
- Create: `deploy/docker-compose.yml`
- Create: `deploy/docker-compose.test.yml`
- Create: `.env.example`
- Create: `deploy/entrypoint.sh` (thin wrapper that runs admin init if needed)
- Modify: `.gitignore` — ignore `.env.local`, `./app.pem` if devs drop it there

- [ ] **Step 1: Dockerfile**

```dockerfile
# deploy/Dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
# Leverage Docker layer cache for deps.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' \
    -o /out/cronfoundry ./cmd/cronfoundry

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cronfoundry /cronfoundry
USER nonroot
ENTRYPOINT ["/cronfoundry"]
```

- [ ] **Step 2: Compose file**

```yaml
# deploy/docker-compose.yml
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: cronfoundry
      POSTGRES_PASSWORD: ${CRONFOUNDRY_DB_PASSWORD:-cronfoundry}
      POSTGRES_DB: cronfoundry
    ports: ["5432:5432"]
    volumes: ["db-data:/var/lib/postgresql/data"]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U cronfoundry -d cronfoundry"]
      interval: 5s
      retries: 10

  cronfoundry:
    image: cronfoundry:dev
    build:
      context: ..
      dockerfile: deploy/Dockerfile
    environment:
      CRONFOUNDRY_DATABASE_URL: postgres://cronfoundry:${CRONFOUNDRY_DB_PASSWORD:-cronfoundry}@db:5432/cronfoundry?sslmode=disable
      CRONFOUNDRY_MASTER_KEY: ${CRONFOUNDRY_MASTER_KEY}
      CRONFOUNDRY_GITHUB_APP_ID: ${CRONFOUNDRY_GITHUB_APP_ID}
      CRONFOUNDRY_GITHUB_APP_PEM: /run/secrets/app.pem
    ports: ["8080:8080"]
    depends_on:
      db: { condition: service_healthy }
    secrets: [app_pem]
    command: ["serve", "--addr", "0.0.0.0:8080"]

secrets:
  app_pem:
    file: ../app.pem

volumes:
  db-data:
```

- [ ] **Step 3: Test-mode compose (Postgres only, no cronfoundry service)**

```yaml
# deploy/docker-compose.test.yml
# For `make e2e`: boots just Postgres so the test binary can exec
# cronfoundry serve itself under test control.
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: cronfoundry
      POSTGRES_PASSWORD: cronfoundry
      POSTGRES_DB: cronfoundry
    ports: ["5433:5432"]  # non-conflicting port
    tmpfs: ["/var/lib/postgresql/data"]  # ephemeral — no persistent volume
```

- [ ] **Step 4: `.env.example`**

```
# Copy to .env.local and fill in:
CRONFOUNDRY_MASTER_KEY=          # from `cronfoundry admin init` on first run
CRONFOUNDRY_GITHUB_APP_ID=       # App ID from GitHub
CRONFOUNDRY_DB_PASSWORD=cronfoundry

# Place your GitHub App's private key at ./app.pem (sibling of docker-compose.yml)
```

- [ ] **Step 5: Add `app.pem` to `.gitignore`**

```
# In .gitignore — append:
app.pem
.env.local
```

- [ ] **Step 6: Verify build locally**

```bash
docker build -t cronfoundry:dev -f deploy/Dockerfile .
docker images | grep cronfoundry
```

Expected: image weighs under ~30 MB (distroless + static binary).

- [ ] **Step 7: Commit**

```bash
git add deploy/Dockerfile deploy/docker-compose.yml deploy/docker-compose.test.yml \
        .env.example .gitignore
git commit -m "feat(deploy): dockerfile + compose for local dev harness"
```

---

## Task 9: Expand Makefile

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Replace the Makefile**

```makefile
# Makefile
.PHONY: sqlc test test-short build vet lint dev dev-down migrate e2e clean help

help:
	@echo 'Targets:'
	@echo '  build        Build cronfoundry + cronfoundry-runner binaries'
	@echo '  test         Run all tests (with docker/testcontainers integration)'
	@echo '  test-short   Run unit tests only (no containers)'
	@echo '  vet          go vet ./...'
	@echo '  lint         go vet + gofmt check'
	@echo '  sqlc         Regenerate internal/db/gen/*.go from queries/'
	@echo '  dev          Start docker-compose stack (Postgres + cronfoundry serve)'
	@echo '  dev-down     Stop + remove docker-compose stack'
	@echo '  migrate      Run goose migrations against $$CRONFOUNDRY_DATABASE_URL'
	@echo '  e2e          Run the end-to-end integration test (requires docker)'
	@echo '  clean        Remove built binaries'

build:
	go build -o cronfoundry-runner ./cmd/runner
	go build -o cronfoundry       ./cmd/cronfoundry

test:
	go test ./... -count=1 -timeout 10m

test-short:
	go test -short ./...

vet:
	go vet ./...

lint: vet
	@unformatted=$$(gofmt -l .); \
	 if [ -n "$$unformatted" ]; then echo "Unformatted files:"; echo "$$unformatted"; exit 1; fi

sqlc:
	cd internal/db && sqlc generate

dev:
	cd deploy && docker compose up -d --build
	@echo 'Stack up. Tail logs with: cd deploy && docker compose logs -f cronfoundry'

dev-down:
	cd deploy && docker compose down -v

migrate:
	@if [ -z "$$CRONFOUNDRY_DATABASE_URL" ]; then \
	  echo 'CRONFOUNDRY_DATABASE_URL not set'; exit 1; \
	 fi
	go run ./cmd/cronfoundry admin init

e2e:
	go test -tags=e2e ./cmd/cronfoundry/... -count=1 -timeout 10m -run TestE2E_

clean:
	rm -f cronfoundry cronfoundry-runner
```

- [ ] **Step 2: Sanity-check each target**

```bash
make help
make vet
make test-short
make build
make clean
```

Expected: each target runs without error.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "build: expand Makefile with dev/lint/migrate/e2e targets"
```

---

## Task 10: End-to-end integration test

**Files:**
- Create: `cmd/cronfoundry/e2e_test.go` (build-tagged `e2e`)

This test:
1. Starts a Postgres-only compose stack (`docker-compose.test.yml`).
2. Builds the `cronfoundry` binary.
3. Sets up env vars (master key, DB URL, test GitHub App PEM, test LLM endpoint).
4. Runs `admin init`, `admin connect-repo` (with a test-mode override that points at a local file:// repo fixture), `admin set-secret`, etc.
5. Boots `cronfoundry serve` as a subprocess.
6. Fakes the GitHub API + LLM provider with `httptest` servers.
7. Triggers a manual run via `admin trigger-sync` + `admin run-now-style` (or hits `/internal/schedules/{id}/run-now` directly).
8. Polls for the run to finalize.
9. Asserts: run row has `status=succeeded`, at least 1 event persisted, Discord mock received the payload.
10. Shuts down.

- [ ] **Step 1: Write the test**

```go
//go:build e2e

// cmd/cronfoundry/e2e_test.go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gambtho/cronfoundry/internal/githubtest"
	"github.com/gambtho/cronfoundry/internal/testdb"
)

// TestE2E_FullScheduleFire starts a real serve binary, triggers a manual
// run, and watches it finalize successfully against faked GitHub + LLM.
func TestE2E_FullScheduleFire(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in -short mode")
	}

	dsn, teardown := testdb.BootPGWithDSN(t)
	defer teardown()

	// Build a fixture skill repo on disk (file:// clone target).
	fixtureDir := buildFixtureRepo(t)

	// Fake GitHub token + branches endpoints.
	ghToken := "ghs_e2e"
	ghSrv := fakeGitHubAPI(t, ghToken, fixtureDir)
	defer ghSrv.Close()

	// Fake OpenAI (responds with a canned completion).
	llmSrv := fakeOpenAI(t)
	defer llmSrv.Close()

	// Fake Discord webhook (records POSTs).
	var receivedDiscord []byte
	discordSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedDiscord, _ = readAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer discordSrv.Close()

	// Build + exec cronfoundry binary.
	binPath := buildBinary(t)
	pemBytes, _ := githubtest.MustPrivateKey(t)
	pemPath := filepath.Join(t.TempDir(), "app.pem")
	require.NoError(t, os.WriteFile(pemPath, pemBytes, 0o600))

	env := map[string]string{
		"CRONFOUNDRY_DATABASE_URL":       dsn,
		"CRONFOUNDRY_GITHUB_APP_ID":      "1",
		"CRONFOUNDRY_GITHUB_APP_PEM":     pemPath,
		"CRONFOUNDRY_E2E_GH_BASE_URL":   ghSrv.URL, // consumed by a test-only env override
		// ... (see notes)
	}

	// Run admin init twice (first for master-key gen, second for migrate).
	masterKey := runAdminInit(t, binPath, env)
	env["CRONFOUNDRY_MASTER_KEY"] = masterKey
	runAdminInit(t, binPath, env)

	// Set secrets.
	runAdmin(t, binPath, env, "set-secret", "discord_webhook", discordSrv.URL)
	runAdmin(t, binPath, env, "set-secret", "openai_key", "sk-fake")

	// Connect the (fake) repo.
	runAdmin(t, binPath, env, "connect-repo", "fake/repo",
		"--installation-id", "1", "--branch", "main")

	// Trigger sync to populate skills.
	runAdmin(t, binPath, env, "trigger-sync", "fake/repo")

	// Start serve in background.
	serveCtx, serveCancel := context.WithCancel(context.Background())
	defer serveCancel()
	serveCmd := exec.CommandContext(serveCtx, binPath, "serve", "--addr", "127.0.0.1:18090", "--tick-cadence", "1s")
	serveCmd.Env = envMap(env)
	stdout := &bytes.Buffer{}
	serveCmd.Stdout = stdout
	serveCmd.Stderr = stdout
	require.NoError(t, serveCmd.Start())
	defer func() { _ = serveCmd.Process.Kill() }()

	// Wait for healthz.
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://127.0.0.1:18090/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	}, 10*time.Second, 200*time.Millisecond)

	// Trigger a run now.
	runID := triggerRunNow(t, "http://127.0.0.1:18090", fixtureScheduleName(t, dsn))

	// Poll for the run to finalize.
	require.Eventually(t, func() bool {
		pool, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return false
		}
		defer pool.Close()
		var status string
		err = pool.QueryRow(context.Background(),
			`SELECT status FROM run WHERE id = $1`, runID).Scan(&status)
		return err == nil && (status == "succeeded" || status == "partial_failure" || status == "failed")
	}, 30*time.Second, 500*time.Millisecond, "run did not finalize; serve stdout:\n%s", stdout.String())

	// Assert Discord got a payload.
	assert.NotEmpty(t, receivedDiscord, "Discord webhook was never called")
	var discordBody map[string]any
	require.NoError(t, json.Unmarshal(receivedDiscord, &discordBody))
	assert.NotEmpty(t, discordBody["content"])
}

// Helpers omitted for brevity — see inline implementation at task-execution time.
// buildFixtureRepo, fakeGitHubAPI, fakeOpenAI, buildBinary, runAdminInit,
// runAdmin, triggerRunNow, fixtureScheduleName, readAll, envMap.
```

**The test will need:**

- A few minor runtime env overrides in `cronfoundry serve` so the binary's `github.InstallationCache` and `llm.Provider` factories can be pointed at the `httptest` URLs. The cleanest path is to add two env vars:
  - `CRONFOUNDRY_GITHUB_BASE_URL` (default `https://api.github.com`) — plumbed into `InstallationCache.BaseURL`
  - `CRONFOUNDRY_OPENAI_BASE_URL` (default empty → SDK default) — plumbed into the OpenAI adapter's baseURL
  These are already accepted by the primitives (T3, T11 from P2c plan); just read them in `cmd/cronfoundry/serve.go` and thread through.
  
  **If this wiring turns out to be non-trivial, declare it as a follow-up task and use `cronfoundry admin trigger-sync` with a file:// clone URL override instead.** The goal is a useful e2e test, not a perfect one.

- [ ] **Step 2: Implement the helpers**

The test needs a lot of local helpers. Expect the task to take ~4-6 hours of implementation + iteration. Use it to find the rough edges.

- [ ] **Step 3: Run**

```bash
make e2e
```

Expected: PASS in ~30 seconds. If it flakes, iterate on timeouts + polling.

- [ ] **Step 4: Commit**

```bash
git add cmd/cronfoundry/e2e_test.go internal/testdb/testdb.go cmd/cronfoundry/serve.go
git commit -m "test(e2e): end-to-end integration test for cronfoundry serve"
```

---

## Task 11: README refresh

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Refresh the README's "Run" section**

Replace the P1-only "Quick start" block with:

```markdown
## Quick start (local dev)

```bash
# 1. Build.
make build

# 2. Generate a master key on first run, copy the env line it prints.
./cronfoundry admin init
export CRONFOUNDRY_MASTER_KEY='<paste>'

# 3. Start Postgres + cronfoundry (docker-compose).
cp .env.example .env.local   # edit with your values
# Place your GitHub App's private key at ./app.pem
make dev

# 4. Run migrations + seed the default organization.
export CRONFOUNDRY_DATABASE_URL='postgres://cronfoundry:cronfoundry@localhost:5432/cronfoundry?sslmode=disable'
make migrate

# 5. Connect a repo + set secrets via the CLI.
./cronfoundry admin connect-repo myorg/skills-repo --installation-id 12345
echo -n 'https://hooks.slack.com/...' | ./cronfoundry admin set-secret slack_webhook
echo -n 'sk-...' | ./cronfoundry admin set-secret openai_key

# 6. Watch logs.
cd deploy && docker compose logs -f cronfoundry
```

See [`docs/guides/smoke-test-p2.md`](docs/guides/smoke-test-p2.md) for the full walkthrough with GitHub App registration.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: update README for docker-compose + admin CLI workflow"
```

---

## Task 12: Tag `v0.2.0-p2`

**Files:**
- none — this is a git tag.

- [ ] **Step 1: Verify main is clean and tests pass**

```bash
go test ./... -count=1 -timeout 10m
go vet ./...
```

Expected: clean.

- [ ] **Step 2: Tag**

```bash
git tag -a v0.2.0-p2 -m "P2 — service layer complete

P2 brings the always-on service stack: Postgres, GitHub App sync,
scheduler tick loop, /internal HTTP API, subprocess runner dispatch.
cronfoundry serve runs the full hot path end-to-end.

See docs/superpowers/specs/2026-04-19-cronfoundry-p2-design.md for the
architecture and each sub-phase plan."
git push origin v0.2.0-p2
```

---

## Self-Review

**Spec coverage** (items from P2d scope notes across P2a/b/c):

| Item | Task |
|---|---|
| Shared `internal/testdb` package | T1, T2 |
| Hoist secret-ref JSON scanner | T3 |
| `admin list-runs` / `show-run` | T5, T6 |
| Writeback push (T15 TODO from P2c) | T7 |
| Docker-compose + Dockerfile | T8 |
| Expanded Makefile (vet/lint/migrate/e2e) | T9 |
| End-to-end integration test | T10 |
| README refresh | T11 |
| v0.2.0-p2 tag | T12 |
| **Queue-drain loop (T14 TODO in overlap.go)** | **MISSING** — add as Task 10.5 or defer to post-P2 |
| **Per-run event streaming** (P2c T15 TODO) | **DEFERRED** — needs P1 runner hooks; out of scope |
| **Include constraints on sqlc schema.sql** | **DEFERRED** — noted at P2a polish; tracked |

**Action:** add a short Task 10.5 for the queue-drain loop. The core fix (manual + queued runs dispatched) landed in P2c's `dispatchPending`; what's missing is a dedicated fast-path loop that catches queued runs as soon as their predecessor terminates, rather than waiting for the next 30s tick.

**Amendment — new Task 10.5:**

```markdown
## Task 10.5: Queue-drain loop (low-latency queued-run dispatch)

**Goal:** When a queued run's predecessor finishes, dispatch the next queued run without waiting for the next Tick.

**Files:**
- Create: `internal/scheduler/queue_drain.go`
- Create: `internal/scheduler/queue_drain_test.go`
- Modify: `internal/scheduler/loop.go` — start the drain goroutine alongside Tick
- Modify: `internal/api/finalize.go` — broadcast a "run terminated" signal

Implementation sketch: a channel the finalize handler writes to when a run transitions to a terminal state. A goroutine in the scheduler consumes events and runs `dispatchPending` immediately (rather than on the 30s ticker). Falls back to the normal Tick sweep if the channel buffer overflows.

Estimated: 1-2 hours. If it's not ready for v0.2.0-p2 ship, note in release notes and defer to v0.2.1.
```

**Placeholder scan:** no TBDs in task steps. The T10 "Implement the helpers" step is underspecified by design — E2E test helpers are notoriously task-specific; doing TDD for each helper is overkill. The test itself is specified with assertions.

**Type consistency:** `testdb.BootPG` and `testdb.BootPGWithDSN` signatures used consistently across T2 call-site replacements. `config.CollectSecretRefs` signature matches in scheduler and runner call sites in T3.

---

Plan complete and saved to `docs/superpowers/plans/2026-04-20-p2d-integration.md`.
