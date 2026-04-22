# Auto-Pause on Consecutive Failures Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the auto-pause feature specified in `docs/superpowers/specs/2026-04-22-auto-pause-design.md`: after N consecutive `failed` scheduled runs, flip `schedule.enabled=false` and stamp observability metadata on the row; operators re-enable manually.

**Architecture:** Additive to the existing run-finalize path. A new `evaluateAutoPause` function runs in `internal/api` after the finalize transaction commits, in its own short-lived transaction. Four new columns on `schedule`, propagated through the existing config → sync → upsert pipeline. No new services, no new workers.

**Tech Stack:** Go 1.22+, pgx, sqlc, goose, stretchr/testify, React + TypeScript (Dashboard card tweak only).

**Pre-flight:**
- Work happens on branch `spec/auto-pause` in worktree `.worktrees/spec-auto-pause`.
- Run `go test ./... -timeout 120s` (or the `make test` target if docker is available; `make test-short` for quick iteration) before each commit.
- Run `go vet ./...` before each commit.
- After modifying anything under `internal/db/queries/` or `internal/db/migrations/`, run `make sqlc` and commit the regenerated files in the same commit as the query change.
- After modifying anything under `web/src/`, run `cd web && npm run build` to catch type errors and produce a fresh bundle.

---

## File structure (touched)

| Path | Responsibility | Action |
| --- | --- | --- |
| `internal/db/migrations/20260422000001_auto_pause.sql` | Schema migration | Create |
| `internal/db/schema.sql` | sqlc introspection cache | Modify (append migration's up-block) |
| `internal/db/queries/schedule.sql` | Schedule queries | Modify: extend `UpsertSchedule`, rewrite `SetScheduleEnabled`, add `GetScheduleAutoPauseConfig`, `AutoPauseSchedule` |
| `internal/db/queries/run.sql` | Run queries | Modify: add `ListRecentTerminalScheduledRuns` |
| `internal/db/gen/**` | sqlc-generated code | Regenerate via `make sqlc` |
| `internal/config/manifest.go` | YAML parse + validate | Modify: add `AutoPauseConfig`, field on `Schedule`, validation rule |
| `internal/config/manifest_test.go` | Config tests | Modify: add auto_pause parsing / validation cases |
| `internal/sync/upsert.go` | YAML → DB reconcile | Modify: pass `auto_pause_after` into `UpsertSchedule` |
| `internal/sync/upsert_test.go` | Sync tests | Modify: assert `auto_pause_after` round-trips |
| `internal/api/finalize.go` | `POST /internal/runs/:id/finalize` | Modify: call `evaluateAutoPause` after `FinalizeRun` succeeds |
| `internal/api/finalize_autopause.go` | **NEW** — `evaluateAutoPause` and `DefaultAutoPauseAfter` | Create |
| `internal/api/finalize_autopause_test.go` | **NEW** — unit tests for `evaluateAutoPause` | Create |
| `internal/api/finalize_test.go` | Existing integration test | Modify: add one case covering finalize → auto-pause end-to-end |
| `internal/webapi/schedules_test.go` | Resume handler tests | Modify: assert auto-pause columns clear + `last_enabled_at` bumps on resume |
| `web/src/lib/types.ts` | Shared TS types | Modify: add new fields to `Schedule` |
| `web/src/pages/Dashboard.tsx` | Schedule cards | Modify: distinguish auto-pause badge; show reason + relative time |

---

## Task 1: Add migration for the four new schedule columns

**Files:**
- Create: `internal/db/migrations/20260422000001_auto_pause.sql`
- Modify: `internal/db/schema.sql`

- [ ] **Step 1: Write the migration file**

Create `internal/db/migrations/20260422000001_auto_pause.sql`:

```sql
-- +goose Up
ALTER TABLE schedule
  ADD COLUMN auto_pause_after   int,
  ADD COLUMN auto_paused_at     timestamptz,
  ADD COLUMN auto_pause_reason  text,
  ADD COLUMN last_enabled_at    timestamptz NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE schedule
  DROP COLUMN auto_pause_after,
  DROP COLUMN auto_paused_at,
  DROP COLUMN auto_pause_reason,
  DROP COLUMN last_enabled_at;
```

- [ ] **Step 2: Append the Up block to `internal/db/schema.sql`**

Find the existing `CREATE TABLE schedule (...)` block and add the four new columns inline. `internal/db/schema.sql` is a flat, ordered concatenation of every migration's up-block — the four lines join the existing column list:

```sql
    ...
    env_json            jsonb NOT NULL DEFAULT '{}'::jsonb,
    auto_pause_after    int,
    auto_paused_at      timestamptz,
    auto_pause_reason   text,
    last_enabled_at     timestamptz NOT NULL DEFAULT now(),
    next_fire_at        timestamptz,
    ...
```

(Match the style of adjacent columns — 4-space indent, lower-case type names, aligned defaults.)

- [ ] **Step 3: Verify the migration applies cleanly**

Run the existing migration-test suite:

```bash
go test ./internal/db/... -run Migrate -timeout 120s
```

Expected: PASS. This boots a temporary Postgres via testcontainers and applies every migration in order.

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations/20260422000001_auto_pause.sql internal/db/schema.sql
git commit -m "feat(db): add auto-pause columns to schedule

Adds auto_pause_after, auto_paused_at, auto_pause_reason, last_enabled_at.
last_enabled_at defaults to now() so existing rows get a fresh anti-flap
window on migration apply."
```

---

## Task 2: Regenerate sqlc code for new columns

**Files:**
- Modify: `internal/db/gen/models.go` (auto-generated)
- Modify: `internal/db/gen/schedule.sql.go` (auto-generated)

- [ ] **Step 1: Run sqlc**

```bash
make sqlc
```

- [ ] **Step 2: Inspect the regenerated `Schedule` model**

`grep -n "AutoPauseAfter\|AutoPausedAt\|AutoPauseReason\|LastEnabledAt" internal/db/gen/models.go`

Expected: four new fields on the `Schedule` struct. `AutoPauseAfter` is `*int32` (nullable int), `AutoPausedAt` / `AutoPauseReason` are `*time.Time` / `*string` (nullable), `LastEnabledAt` is `time.Time` (NOT NULL).

- [ ] **Step 3: Verify `go build ./...` still succeeds**

```bash
go build ./...
```

Expected: no compile errors. No callers reference the new fields yet, so everything builds clean.

- [ ] **Step 4: Commit**

```bash
git add internal/db/gen/
git commit -m "chore(db): regenerate sqlc for auto-pause columns"
```

---

## Task 3: Add `AutoPauseConfig` to manifest parsing

**Files:**
- Modify: `internal/config/manifest.go`
- Modify: `internal/config/manifest_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/manifest_test.go`:

```go
func TestParseManifest_AutoPauseAfter(t *testing.T) {
	cases := []struct {
		name  string
		yaml  string
		want  *int    // nil means the AutoPause field should be nil
		err   string  // non-empty expected substring means expect a Validate() error
	}{
		{
			name: "missing auto_pause → nil",
			yaml: minimalManifest(""),
			want: nil,
		},
		{
			name: "auto_pause.after: 3",
			yaml: minimalManifest("        auto_pause:\n          after: 3\n"),
			want: intPtr(3),
		},
		{
			name: "auto_pause.after: 0 rejected",
			yaml: minimalManifest("        auto_pause:\n          after: 0\n"),
			err:  "auto_pause.after must be >= 1",
		},
		{
			name: "auto_pause.after: -1 rejected",
			yaml: minimalManifest("        auto_pause:\n          after: -1\n"),
			err:  "auto_pause.after must be >= 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(tc.yaml))
			require.NoError(t, err)
			verr := m.Validate()
			if tc.err != "" {
				require.Error(t, verr)
				require.Contains(t, verr.Error(), tc.err)
				return
			}
			require.NoError(t, verr)
			sch := m.Skills[0].Schedules[0]
			if tc.want == nil {
				require.Nil(t, sch.AutoPause)
			} else {
				require.NotNil(t, sch.AutoPause)
				require.Equal(t, *tc.want, sch.AutoPause.After)
			}
		})
	}
}

func intPtr(i int) *int { return &i }

// minimalManifest returns a valid manifest YAML with the given extra lines
// spliced into the single schedule's body. Each extra line must already be
// indented to align with the schedule block.
func minimalManifest(extra string) string {
	return `version: 1
skills:
  - path: skills/hello
    schedules:
      - name: daily
        cron: "0 9 * * *"
        provider: openai
        model: gpt-4o
` + extra
}
```

Also ensure `require` is imported (`"github.com/stretchr/testify/require"`); other tests in this file already use it.

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
go test ./internal/config/ -run TestParseManifest_AutoPauseAfter -v
```

Expected: compile error (`sch.AutoPause` undefined).

- [ ] **Step 3: Add the type and field to `internal/config/manifest.go`**

Inside the existing type block, define:

```go
// AutoPauseConfig controls the auto-pause-on-consecutive-failures behavior.
// If nil, the schedule uses the global default (DefaultAutoPauseAfter).
type AutoPauseConfig struct {
	After int `json:"after"`
}
```

Add `AutoPause *AutoPauseConfig` to the `Schedule` struct (place it with the other optional configs like `Writeback`):

```go
type Schedule struct {
	Name          string              `json:"name"`
	Cron          string              `json:"cron"`
	Timezone      string              `json:"timezone"`
	OverlapPolicy string              `json:"overlap_policy"`
	TimeoutSec    int                 `json:"timeout_sec"`
	Provider      string              `json:"provider"`
	Model         string              `json:"model"`
	Destinations  []Destination       `json:"destinations"`
	Writeback     *WritebackConfig    `json:"writeback,omitempty"`
	Env           map[string]EnvValue `json:"env"`
	AutoPause     *AutoPauseConfig    `json:"auto_pause,omitempty"`
}
```

- [ ] **Step 4: Extend `Manifest.Validate` with the `>= 1` check**

In `Validate()`, inside the existing per-schedule loop (the block that already validates `Cron`, `Provider`, `Model`, `OverlapPolicy`):

```go
if sch.AutoPause != nil && sch.AutoPause.After < 1 {
	return fmt.Errorf("skill %q schedule %q: auto_pause.after must be >= 1 (got %d)",
		s.Path, sch.Name, sch.AutoPause.After)
}
```

- [ ] **Step 5: Run the tests to confirm they pass**

```bash
go test ./internal/config/ -run TestParseManifest_AutoPauseAfter -v
```

Expected: PASS (4 cases).

- [ ] **Step 6: Commit**

```bash
git add internal/config/manifest.go internal/config/manifest_test.go
git commit -m "feat(config): parse auto_pause.after on schedules"
```

---

## Task 4: Thread `auto_pause_after` through `UpsertSchedule`

**Files:**
- Modify: `internal/db/queries/schedule.sql`
- Modify: `internal/db/gen/schedule.sql.go` (regenerated)
- Modify: `internal/sync/upsert.go`
- Modify: `internal/sync/upsert_test.go`

- [ ] **Step 1: Extend `UpsertSchedule` in `internal/db/queries/schedule.sql`**

Replace the existing `UpsertSchedule` block with:

```sql
-- name: UpsertSchedule :one
INSERT INTO schedule (
    org_id, skill_id, name, cron, timezone, overlap_policy, timeout_sec,
    enabled, provider, model, llm_secret_ref, llm_endpoint, llm_deployment,
    destinations_json, writeback_json, env_json, auto_pause_after, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, now())
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
      auto_pause_after  = EXCLUDED.auto_pause_after,
      updated_at        = now()
RETURNING *;
```

The only new line in the DO UPDATE block is `auto_pause_after = EXCLUDED.auto_pause_after,`. Keep the surrounding lines identical to the pre-existing query (including `enabled = EXCLUDED.enabled` if present). No other change to the DO UPDATE's semantics.

- [ ] **Step 2: Regenerate sqlc**

```bash
make sqlc
```

Expected: `UpsertScheduleParams` now has a new `AutoPauseAfter *int32` field.

- [ ] **Step 3: Wire it through `internal/sync/upsert.go`**

Find the existing `q.UpsertSchedule(ctx, dbgen.UpsertScheduleParams{...})` call (around line 107). Just before it, compute the nullable:

```go
var autoPauseAfter *int32
if sch.AutoPause != nil {
	v := int32(sch.AutoPause.After)
	autoPauseAfter = &v
}
```

Add `AutoPauseAfter: autoPauseAfter,` to the params struct literal.

- [ ] **Step 4: Write the failing sync test**

Append to `internal/sync/upsert_test.go` a test similar to existing sync tests. It should:

1. Seed org + repo + skill in Postgres via `testdb.BootPG(t)`.
2. Build a manifest with one schedule carrying `AutoPause: &config.AutoPauseConfig{After: 3}`.
3. Call `UpsertSkillsAndSchedules`.
4. `SELECT auto_pause_after FROM schedule WHERE name = 'daily'` → expect `3`.
5. Re-run upsert with `AutoPause: nil`.
6. `SELECT auto_pause_after` → expect NULL.

Model the shape of the test on the nearest existing sync test (for example `TestUpsert_SchedulesUpsertAndDisable`).

- [ ] **Step 5: Run tests**

```bash
go test ./internal/sync/ -run AutoPause -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/queries/schedule.sql internal/db/gen/ internal/sync/upsert.go internal/sync/upsert_test.go
git commit -m "feat(sync): persist auto_pause_after from manifest"
```

---

## Task 5: Rewrite `SetScheduleEnabled` to handle resume semantics

**Files:**
- Modify: `internal/db/queries/schedule.sql`
- Modify: `internal/db/gen/schedule.sql.go` (regenerated)
- Modify: `internal/webapi/schedules_test.go`

- [ ] **Step 1: Replace `SetScheduleEnabled`**

In `internal/db/queries/schedule.sql`, replace the existing `SetScheduleEnabled` block with:

```sql
-- name: SetScheduleEnabled :one
-- On enable: clear any auto-pause state and bump last_enabled_at to reset the
-- consecutive-failure anti-flap window. On disable: leave the auto-pause
-- columns untouched (a user-initiated pause should not masquerade as an
-- auto-pause if one happens to already be set, though in practice they can't
-- co-exist because enabled flips from true to false).
UPDATE schedule
SET enabled = $2,
    auto_paused_at    = CASE WHEN $2 THEN NULL              ELSE auto_paused_at    END,
    auto_pause_reason = CASE WHEN $2 THEN NULL              ELSE auto_pause_reason END,
    last_enabled_at   = CASE WHEN $2 THEN now()             ELSE last_enabled_at   END,
    updated_at        = now()
WHERE id = $1
  AND org_id = $3
RETURNING *;
```

- [ ] **Step 2: Regenerate sqlc**

```bash
make sqlc
```

- [ ] **Step 3: Confirm the webapi handler still compiles**

```bash
go build ./internal/webapi/...
```

No code changes needed in `internal/webapi/schedules.go` — the handler calls `SetScheduleEnabled` with the same params (`ID`, `Enabled`, `OrgID`), and the query handles the CASE logic internally.

- [ ] **Step 4: Write the failing handler test**

Append to `internal/webapi/schedules_test.go`:

```go
func TestResume_ClearsAutoPauseAndBumpsLastEnabledAt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	// Seed a schedule that is already auto-paused.
	scheduleID := seedAutoPausedSchedule(t, pool)

	// Capture last_enabled_at before resume.
	var beforeLEA time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT last_enabled_at FROM schedule WHERE id = $1`, scheduleID).Scan(&beforeLEA))

	// Simulate a small tick of wall-clock so "bumped" is observable.
	time.Sleep(10 * time.Millisecond)

	// Perform resume via the real handler.
	srv := newTestServer(t, pool)      // existing helper in this test file
	defer srv.Close()
	req := authedReq(t, "POST", srv.URL+"/api/schedules/"+scheduleID.String()+"/resume", nil)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify: enabled true, reason/at cleared, last_enabled_at bumped.
	var enabled bool
	var pausedAt *time.Time
	var reason *string
	var afterLEA time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT enabled, auto_paused_at, auto_pause_reason, last_enabled_at FROM schedule WHERE id = $1`,
		scheduleID).Scan(&enabled, &pausedAt, &reason, &afterLEA))
	assert.True(t, enabled)
	assert.Nil(t, pausedAt)
	assert.Nil(t, reason)
	assert.True(t, afterLEA.After(beforeLEA), "last_enabled_at should advance on resume")
}
```

You will need to add a helper `seedAutoPausedSchedule` that INSERTs an org, repo, skill, and a schedule with `enabled=false, auto_paused_at=now(), auto_pause_reason='test'`. Model it on existing seed helpers in nearby `_test.go` files (search for `seedSchedule` in the package).

- [ ] **Step 5: Run tests**

```bash
go test ./internal/webapi/ -run TestResume_ClearsAutoPauseAndBumpsLastEnabledAt -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/queries/schedule.sql internal/db/gen/ internal/webapi/schedules_test.go
git commit -m "feat(webapi): clear auto-pause state and bump last_enabled_at on resume"
```

---

## Task 6: Add read queries for `evaluateAutoPause`

**Files:**
- Modify: `internal/db/queries/schedule.sql`
- Modify: `internal/db/queries/run.sql`
- Modify: `internal/db/gen/schedule.sql.go` (regenerated)
- Modify: `internal/db/gen/run.sql.go` (regenerated)

- [ ] **Step 1: Add `GetScheduleAutoPauseConfig` to `internal/db/queries/schedule.sql`**

Append:

```sql
-- name: GetScheduleAutoPauseConfig :one
-- Returns the fields evaluateAutoPause needs to decide whether to trigger a
-- pause and emit audit/run_event rows: org_id (for audit), the per-schedule
-- threshold override (nullable), and the anti-flap window boundary.
-- `enabled` is returned for tests/debug; the pause query guards on it
-- independently via `WHERE enabled = true`.
SELECT org_id, auto_pause_after, last_enabled_at, enabled
FROM schedule
WHERE id = $1;
```

- [ ] **Step 2: Add `AutoPauseSchedule` to `internal/db/queries/schedule.sql`**

Append:

```sql
-- name: AutoPauseSchedule :execrows
-- Idempotent conditional pause. Returns the number of rows affected so the
-- caller can distinguish "we paused it" (1) from "someone else already paused
-- it" (0, in which case the caller must not emit duplicate audit rows).
UPDATE schedule
SET enabled           = false,
    auto_paused_at    = now(),
    auto_pause_reason = $2,
    updated_at        = now()
WHERE id = $1
  AND enabled = true;
```

- [ ] **Step 3: Add `ListRecentTerminalScheduledRuns` to `internal/db/queries/run.sql`**

Append:

```sql
-- name: ListRecentTerminalScheduledRuns :many
-- Used by evaluateAutoPause. Returns the last N terminal scheduled runs for
-- a schedule, within the anti-flap window defined by last_enabled_at.
-- Uses `created_at` (NOT NULL, matches the existing run_schedule_created_idx)
-- so failed-before-dispatch runs (started_at NULL) are still counted.
-- id DESC is a stable tie-breaker when two runs share created_at (tests /
-- tight scheduler clocks).
SELECT status
FROM run
WHERE schedule_id = $1
  AND fire_reason = 'schedule'
  AND status IN ('succeeded', 'partial_failure', 'failed')
  AND created_at >= $2
ORDER BY created_at DESC, id DESC
LIMIT $3;
```

- [ ] **Step 4: Regenerate sqlc**

```bash
make sqlc
```

- [ ] **Step 5: `go build ./...` and verify no callers broke**

```bash
go build ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/db/queries/schedule.sql internal/db/queries/run.sql internal/db/gen/
git commit -m "feat(db): add queries for auto-pause evaluation"
```

---

## Task 7: Implement `evaluateAutoPause`

**Files:**
- Create: `internal/api/finalize_autopause.go`
- Create: `internal/api/finalize_autopause_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/finalize_autopause_test.go`:

```go
package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
	"github.com/gambtho/cronfoundry/internal/testdb"
)

// apHarness bundles a seeded DB + helpers used by every auto-pause case.
type apHarness struct {
	pool       *pgxpool.Pool
	orgID      pgtype.UUID
	scheduleID pgtype.UUID
	skillID    pgtype.UUID
	enabledAt  time.Time
}

func newAPHarness(t *testing.T, autoPauseAfter *int32) apHarness {
	t.Helper()
	pool, cleanup := testdb.BootPG(t)
	t.Cleanup(cleanup)

	ctx := context.Background()
	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))
	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 42, 'acme', 'widgets', 'main') RETURNING id`, orgID).Scan(&repoID))
	var skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha-1', '{"name":"a"}'::jsonb) RETURNING id`,
		orgID, repoID).Scan(&skillID))

	var scheduleID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO schedule (org_id, skill_id, name, cron, provider, model,
		                     destinations_json, auto_pause_after)
		VALUES ($1, $2, 'daily', '0 9 * * *', 'openai', 'gpt-4o',
		        '[]'::jsonb, $3)
		RETURNING id
	`, orgID, skillID, autoPauseAfter).Scan(&scheduleID))

	var enabledAt time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT last_enabled_at FROM schedule WHERE id = $1`, scheduleID).Scan(&enabledAt))

	return apHarness{pool: pool, orgID: orgID, scheduleID: scheduleID, skillID: skillID, enabledAt: enabledAt}
}

// seedRun creates a single run row; used to build failure histories.
// status must be one of succeeded/partial_failure/failed. fireReason is
// "schedule" or "manual". startedAt is explicit so tests can position
// runs relative to last_enabled_at.
func (h apHarness) seedRun(t *testing.T, status, fireReason string, startedAt time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, h.pool.QueryRow(context.Background(), `
		INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason,
		                 started_at, finished_at, runner_token_hash)
		VALUES ($1, $2, 'abc123', $3, $4, $5, $5, 'hash-for-test')
		RETURNING id
	`, h.orgID, h.scheduleID, status, fireReason, startedAt).Scan(&id))
	return id
}

// assertPaused confirms the schedule is enabled=false with stamped auto-pause
// state and that at least one audit_log row with action='schedule.auto_paused'
// exists for the schedule.
func (h apHarness) assertPaused(t *testing.T) {
	t.Helper()
	var enabled bool
	var pausedAt *time.Time
	var reason *string
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT enabled, auto_paused_at, auto_pause_reason FROM schedule WHERE id = $1`,
		h.scheduleID).Scan(&enabled, &pausedAt, &reason))
	assert.False(t, enabled, "schedule should be disabled")
	require.NotNil(t, pausedAt, "auto_paused_at should be set")
	require.NotNil(t, reason, "auto_pause_reason should be set")
	assert.Contains(t, *reason, "consecutive failed runs")

	var n int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = 'schedule.auto_paused' AND target_id = $1`,
		h.scheduleID).Scan(&n))
	assert.GreaterOrEqual(t, n, 1, "at least one schedule.auto_paused audit row")
}

// assertNotPaused confirms the schedule is still enabled and no auto-pause
// state is set.
func (h apHarness) assertNotPaused(t *testing.T) {
	t.Helper()
	var enabled bool
	var pausedAt *time.Time
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT enabled, auto_paused_at FROM schedule WHERE id = $1`,
		h.scheduleID).Scan(&enabled, &pausedAt))
	assert.True(t, enabled, "schedule should still be enabled")
	assert.Nil(t, pausedAt, "auto_paused_at should still be null")
}

func TestEvaluateAutoPause_TriggersAtDefaultThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil) // threshold = DefaultAutoPauseAfter (5)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 4; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "schedule", base.Add(5*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertPaused(t)
}

func TestEvaluateAutoPause_SuccessBreaksStreak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 3; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	h.seedRun(t, "succeeded", "schedule", base.Add(3*time.Minute))
	last := h.seedRun(t, "failed", "schedule", base.Add(4*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertNotPaused(t)
}

func TestEvaluateAutoPause_PartialFailureBreaksStreak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 3; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	h.seedRun(t, "partial_failure", "schedule", base.Add(3*time.Minute))
	last := h.seedRun(t, "failed", "schedule", base.Add(4*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertNotPaused(t)
}

func TestEvaluateAutoPause_ManualFailureExcluded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 4; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "manual", base.Add(5*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "manual")
	require.NoError(t, err)

	h.assertNotPaused(t) // manual run doesn't trigger evaluation at all
}

func TestEvaluateAutoPause_AntiFlapWindowExcludesOldRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	// One run BEFORE the enable window should be excluded.
	h.seedRun(t, "failed", "schedule", h.enabledAt.Add(-time.Hour))
	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 3; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "schedule", base.Add(4*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertNotPaused(t) // only 4 in-window runs, not 5
}

func TestEvaluateAutoPause_PerScheduleOverride(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	threshold := int32(3)
	h := newAPHarness(t, &threshold)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 2; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "schedule", base.Add(3*time.Minute))

	err := evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule")
	require.NoError(t, err)

	h.assertPaused(t)
}

func TestEvaluateAutoPause_IdempotentWhenAlreadyPaused(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	base := h.enabledAt.Add(time.Second)
	for i := 0; i < 4; i++ {
		h.seedRun(t, "failed", "schedule", base.Add(time.Duration(i)*time.Minute))
	}
	last := h.seedRun(t, "failed", "schedule", base.Add(5*time.Minute))

	// First call pauses.
	require.NoError(t, evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule"))
	h.assertPaused(t)

	var countBefore int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action='schedule.auto_paused' AND target_id=$1`,
		h.scheduleID).Scan(&countBefore))

	// Second call: schedule is already paused; UPDATE affects 0 rows; no new audit.
	require.NoError(t, evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "failed", "schedule"))

	var countAfter int
	require.NoError(t, h.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action='schedule.auto_paused' AND target_id=$1`,
		h.scheduleID).Scan(&countAfter))
	assert.Equal(t, countBefore, countAfter, "no duplicate audit rows on re-pause")
}

func TestEvaluateAutoPause_NoOpForNonFailedOrNonScheduled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	h := newAPHarness(t, nil)

	// Succeeded run: no-op regardless of history.
	last := h.seedRun(t, "succeeded", "schedule", h.enabledAt.Add(time.Second))
	require.NoError(t, evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(last.Bytes), "succeeded", "schedule"))
	h.assertNotPaused(t)

	// Manual failed run: no-op regardless of history.
	manual := h.seedRun(t, "failed", "manual", h.enabledAt.Add(2*time.Second))
	require.NoError(t, evaluateAutoPause(context.Background(), h.pool,
		uuid.UUID(h.scheduleID.Bytes), uuid.UUID(manual.Bytes), "failed", "manual"))
	h.assertNotPaused(t)
}
```

Add `"github.com/jackc/pgx/v5/pgxpool"` to the import block so `apHarness.pool` compiles. Other helpers (`randomMaster`, `bindRunHash`, `seedRun`, `seedRunWithHash`) already live in this package's existing test files (`auth_test.go`, `run_context_test.go`).

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
go test ./internal/api/ -run TestEvaluateAutoPause -v
```

Expected: compile error (`evaluateAutoPause` undefined).

- [ ] **Step 3: Implement `evaluateAutoPause`**

Create `internal/api/finalize_autopause.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gambtho/cronfoundry/internal/audit"
	dbgen "github.com/gambtho/cronfoundry/internal/db/gen"
)

// DefaultAutoPauseAfter is the global default threshold for consecutive
// scheduled-run failures before a schedule is auto-paused. Per-schedule
// overrides from cronfoundry.yaml take precedence.
const DefaultAutoPauseAfter int32 = 5

// evaluateAutoPause decides whether the just-finalized run should trigger
// auto-pause on its schedule. It is called from the run-finalize handler
// AFTER the finalize transaction commits; it opens its own short-lived
// transaction so an evaluation error cannot roll back the load-bearing run
// row. All errors are returned; the caller logs and swallows them.
//
// No-ops unless (fireReason == "schedule" AND runStatus == "failed").
func evaluateAutoPause(
	ctx context.Context,
	pool *pgxpool.Pool,
	scheduleID, runID uuid.UUID,
	runStatus, fireReason string,
) error {
	if fireReason != "schedule" || runStatus != "failed" {
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("evaluateAutoPause: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbgen.New(tx)
	schedPG := pgtype.UUID{Bytes: scheduleID, Valid: true}

	cfg, err := q.GetScheduleAutoPauseConfig(ctx, schedPG)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // schedule was deleted; nothing to do
		}
		return fmt.Errorf("evaluateAutoPause: get config: %w", err)
	}

	threshold := DefaultAutoPauseAfter
	if cfg.AutoPauseAfter != nil && *cfg.AutoPauseAfter >= 1 {
		threshold = *cfg.AutoPauseAfter
	}

	statuses, err := q.ListRecentTerminalScheduledRuns(ctx, dbgen.ListRecentTerminalScheduledRunsParams{
		ScheduleID: schedPG,
		StartedAt:  pgtype.Timestamptz{Time: cfg.LastEnabledAt, Valid: true},
		Limit:      int32(threshold),
	})
	if err != nil {
		return fmt.Errorf("evaluateAutoPause: list recent: %w", err)
	}
	if int32(len(statuses)) < threshold {
		return nil
	}
	for _, s := range statuses {
		if s != "failed" {
			return nil // streak broken
		}
	}

	reason := fmt.Sprintf("%d consecutive failed runs", threshold)
	affected, err := q.AutoPauseSchedule(ctx, dbgen.AutoPauseScheduleParams{
		ID:              schedPG,
		AutoPauseReason: &reason,
	})
	if err != nil {
		return fmt.Errorf("evaluateAutoPause: pause update: %w", err)
	}
	if affected == 0 {
		// Raced with another finalize; it already paused the schedule.
		return nil
	}

	scheduleUUID := scheduleID // local copy for addressable pointer
	runUUID := runID
	if err := audit.Log(ctx, q, audit.Entry{
		OrgID:      cfg.OrgID,
		Actor:      "system",
		Action:     "schedule.auto_paused",
		TargetKind: "schedule",
		TargetID:   &scheduleUUID,
		Detail: map[string]any{
			"threshold":    threshold,
			"last_run_id":  runUUID.String(),
		},
	}); err != nil {
		return fmt.Errorf("evaluateAutoPause: audit: %w", err)
	}

	payload := []byte(fmt.Sprintf(`{"threshold":%d}`, threshold))
	if err := q.InsertRunEvent(ctx, dbgen.InsertRunEventParams{
		RunID:       pgtype.UUID{Bytes: runID, Valid: true},
		Level:       "info",
		EventType:   "schedule.auto_paused",
		PayloadJson: payload,
	}); err != nil {
		return fmt.Errorf("evaluateAutoPause: run_event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("evaluateAutoPause: commit: %w", err)
	}

	slog.Info("auto-pause triggered",
		"schedule_id", scheduleID,
		"threshold", threshold,
		"run_id", runID)
	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
make sqlc
go test ./internal/api/ -run TestEvaluateAutoPause -v
```

Expected: all 8 cases PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/finalize_autopause.go internal/api/finalize_autopause_test.go \
        internal/db/queries/schedule.sql internal/db/gen/
git commit -m "feat(api): implement evaluateAutoPause

Pure function that, given a just-finalized run, decides whether to flip
schedule.enabled=false and stamp auto-pause metadata. Runs in its own
short-lived transaction so evaluation errors cannot poison the run-finalize
write."
```

---

## Task 8: Wire `evaluateAutoPause` into the finalize handler

**Files:**
- Modify: `internal/api/finalize.go`
- Modify: `internal/api/finalize_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `internal/api/finalize_test.go`:

```go
func TestFinalize_TriggersAutoPauseAfterConsecutiveFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	// Seed a schedule with 4 prior failed scheduled runs (inline like the
	// existing seedRunWithHash helper — this package does not expose
	// smaller seedOrg/seedRepo helpers).
	ctx := context.Background()
	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO organization (name) VALUES ('o') RETURNING id`).Scan(&orgID))
	var repoID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO repo_connection (org_id, github_app_install_id, owner, name, default_branch)
		 VALUES ($1, 42, 'acme', 'widgets', 'main') RETURNING id`, orgID).Scan(&repoID))
	var skillID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO skill (org_id, repo_id, path, name, current_sha, frontmatter_json)
		 VALUES ($1, $2, 'skills/a', 'a', 'sha-1', '{"name":"a"}'::jsonb) RETURNING id`,
		orgID, repoID).Scan(&skillID))
	var scheduleID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO schedule (org_id, skill_id, name, cron, provider, model, destinations_json)
		VALUES ($1, $2, 'daily', '0 9 * * *', 'openai', 'gpt-4o', '[]'::jsonb)
		RETURNING id
	`, orgID, skillID).Scan(&scheduleID))

	var enabledAt time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT last_enabled_at FROM schedule WHERE id=$1`, scheduleID).Scan(&enabledAt))

	base := enabledAt.Add(time.Second)
	for i := 0; i < 4; i++ {
		_, err := pool.Exec(context.Background(), `
			INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason,
			                 started_at, finished_at, runner_token_hash)
			VALUES ($1, $2, 'abc', 'failed', 'schedule', $3, $3, 'hash-seed')
		`, orgID, scheduleID, base.Add(time.Duration(i)*time.Minute))
		require.NoError(t, err)
	}

	// Create a 5th pending run under the same org/schedule and finalize it via the handler.
	var runPG pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason, runner_token_hash)
		VALUES ($1, $2, 'sha-1', 'pending', 'schedule', 'placeholder')
		RETURNING id
	`, orgID, scheduleID).Scan(&runPG))
	runID := uuid.UUID(runPG.Bytes)

	signer := token.New(randomMaster(t))
	tok, hash, err := signer.Sign(token.RunClaims{
		RunID:     runID,
		OrgID:     uuid.UUID(orgID.Bytes),
		ExpiresAt: time.Now().Add(time.Minute),
	})
	require.NoError(t, err)
	bindRunHash(t, pool, runID, hash)

	srv := NewServer("127.0.0.1:0", Deps{Pool: pool, Signer: signer})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	body := map[string]any{"status": "failed", "error_kind": "llm_error"}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", ts.URL+"/internal/runs/"+runID.String()+"/finalize", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Assert pause landed.
	var enabled bool
	var pausedAt *time.Time
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT enabled, auto_paused_at FROM schedule WHERE id=$1`, scheduleID).Scan(&enabled, &pausedAt))
	assert.False(t, enabled)
	require.NotNil(t, pausedAt)
}
```

- [ ] **Step 2: Run the test — expect it to FAIL**

```bash
go test ./internal/api/ -run TestFinalize_TriggersAutoPauseAfterConsecutiveFailures -v
```

Expected: FAIL — the schedule is still enabled because `evaluateAutoPause` isn't called yet.

- [ ] **Step 3: Wire `evaluateAutoPause` into `internal/api/finalize.go`**

Find the block where `FinalizeRun` is called. After successful finalize, add:

```go
	row, err := q.FinalizeRun(r.Context(), dbgen.FinalizeRunParams{...})
	if err != nil {
		// ...existing error handling...
	}

	// Auto-pause evaluation runs AFTER finalize has committed, in its own
	// transaction. Errors here are logged and swallowed — the finalize
	// response must not depend on it.
	scheduleUUID := uuid.UUID(row.ScheduleID.Bytes)
	if err := evaluateAutoPause(
		r.Context(), h.deps.Pool,
		scheduleUUID, urlRunID,
		body.Status, row.FireReason,
	); err != nil {
		slog.Warn("finalize: auto-pause evaluation failed (non-fatal)",
			"run_id", urlRunID, "err", err)
	}

	w.WriteHeader(http.StatusNoContent)
```

Note: `FinalizeRun` currently returns a `FinalizeRunRow` or equivalent. Inspect the regenerated `internal/db/gen/run.sql.go` to confirm it exposes `ScheduleID` and `FireReason`. If either is missing from the RETURNING clause, add them:

```sql
-- name: FinalizeRun :one
UPDATE run
SET ...
RETURNING *;  -- already returns * per the existing query; confirm schedule_id + fire_reason are on the generated struct
```

If the existing query uses `RETURNING *` the struct is complete; otherwise extend the RETURNING clause and regen.

- [ ] **Step 4: Re-run the test**

```bash
go test ./internal/api/ -run TestFinalize_TriggersAutoPauseAfterConsecutiveFailures -v
```

Expected: PASS.

- [ ] **Step 5: Run the full API test suite to catch regressions**

```bash
go test ./internal/api/... -timeout 120s
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/finalize.go internal/api/finalize_test.go
git commit -m "feat(api): call evaluateAutoPause after run finalize

Wires the new post-commit evaluation into the POST /internal/runs/:id/finalize
handler. Errors from evaluation are logged and swallowed — the finalize
response must not depend on auto-pause succeeding."
```

---

## Task 9: Expose new schedule fields to the frontend

**Files:**
- Modify: `web/src/lib/types.ts`
- Modify: `web/src/pages/Dashboard.tsx`

- [ ] **Step 1: Extend the `Schedule` TS interface**

In `web/src/lib/types.ts`, add four fields to `Schedule`:

```typescript
export interface Schedule {
  id: string
  skill_id: string
  name: string
  cron: string
  timezone: string
  overlap_policy: string
  timeout_sec: number
  enabled: boolean
  provider: string
  model: string
  next_fire_at: string | null
  auto_pause_after: number | null
  auto_paused_at: string | null
  auto_pause_reason: string | null
  last_enabled_at: string
  skill_path: string
  skill_name: string
  owner: string
  repo_name: string
}
```

- [ ] **Step 2: Add a small relative-time helper if one isn't present**

Check if `web/src/lib/` has a time-ago helper. If not, add to `web/src/lib/api.ts` (or a new `web/src/lib/time.ts`):

```typescript
export function relativeTime(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime()
  const mins = Math.round(diffMs / 60_000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.round(mins / 60)
  if (hours < 48) return `${hours}h ago`
  return `${Math.round(hours / 24)}d ago`
}
```

- [ ] **Step 3: Update the Dashboard badge rendering**

In `web/src/pages/Dashboard.tsx`, find the block that renders `<span>Paused</span>` (currently under `{!s.enabled && ...}`). Replace with:

```tsx
{!s.enabled && s.auto_paused_at ? (
  <span
    title={s.auto_pause_reason ?? undefined}
    className="text-xs px-2 py-1 rounded bg-amber-900 text-amber-200"
  >
    Auto-paused · {relativeTime(s.auto_paused_at)}
  </span>
) : !s.enabled ? (
  <span className="text-xs px-2 py-1 rounded bg-gray-800 text-gray-500">
    Paused
  </span>
) : null}
```

Import the helper at the top of the file:

```typescript
import { relativeTime } from '../lib/api'  // or '../lib/time' if you created the new file
```

- [ ] **Step 4: Verify the web build**

```bash
cd web && npm run build
```

Expected: PASS. TypeScript errors would indicate missing/renamed fields.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/types.ts web/src/lib/api.ts web/src/pages/Dashboard.tsx
# Also add web/src/lib/time.ts if you created it.
git commit -m "feat(web): surface auto-pause state on dashboard

New amber 'Auto-paused · Nm ago' badge distinguishes auto-paused schedules
from user-initiated pauses. Tooltip shows the reason from the server."
```

---

## Task 10: Run the full suite + lint + vet before declaring done

**Files:** none

- [ ] **Step 1: `go vet`**

```bash
go vet ./...
```

Expected: no warnings.

- [ ] **Step 2: Lint**

```bash
make lint
```

Expected: PASS.

- [ ] **Step 3: Full Go test suite**

```bash
go test ./... -count=1 -timeout 10m
```

Expected: PASS. (This runs testcontainer-based tests — needs docker running. For a quick pass without containers use `go test -short ./...`, but the final pre-PR run MUST be without `-short`.)

- [ ] **Step 4: Web build**

```bash
cd web && npm run build
```

Expected: PASS.

- [ ] **Step 5: Manual smoke (optional, strongly recommended)**

Start the dev stack (`make dev`), apply migrations, seed a minimal org + repo + schedule, and drive it via `make e2e` or by hand:

1. Fail 5 consecutive scheduled runs (use `make e2e`-adjacent helpers or drive runner with a broken LLM key).
2. Confirm the schedule shows "Auto-paused · Nm ago" on the Dashboard.
3. Click Resume. Confirm the badge clears and a fresh `last_enabled_at` is set.
4. Fail 4 more runs; confirm NO auto-pause (fresh window counted only after resume).
5. Fail a 5th; confirm auto-pause triggers again.

Optional but meaningful — the feature is about operator-facing behavior.

---

## Known interactions (not addressed in this plan)

**Sync re-enables paused schedules.** The existing `UpsertSchedule` query includes `enabled = EXCLUDED.enabled`, and the sync code always passes `Enabled: true`. That means every `cronfoundry.yaml` push re-enables *both* user-paused and auto-paused schedules. This is existing behavior; fixing it would be a separate scope (the sync layer would need to preserve `enabled = false` and/or clear `auto_paused_at` transactionally). The auto-pause feature still provides value in the common case (no unrelated YAML pushes between the failing runs and the fix), but operators should be aware that a config push is an implicit resume.

If this interaction proves annoying in practice, a follow-up plan can add one of two fixes:
- Stop re-enabling via sync: change the `UpsertSchedule` DO UPDATE block to omit `enabled = EXCLUDED.enabled` (behavior: user-pause and auto-pause survive pushes; intentional YAML-driven re-enables no longer work).
- Clear auto-pause on sync-re-enable: when the upsert fires with `enabled=true`, also clear `auto_paused_at`, `auto_pause_reason`, and bump `last_enabled_at`. Less aggressive; keeps push-as-resume working but resets the anti-flap window.

## Self-review checklist

Before submitting the PR, verify:

- [ ] Spec's **Goals**: 4 goals covered (runaway spend prevention, no false pauses, reuse of `schedule.enabled`, observability).
- [ ] Spec's **Non-Goals**: no automatic resume added, no external notification added, no global config UI added.
- [ ] All 8 `TestEvaluateAutoPause_*` cases exist and pass.
- [ ] Resume handler test asserts `last_enabled_at` bumps and auto-pause columns clear.
- [ ] Migration applies cleanly and down-migrates cleanly.
- [ ] `schema.sql` matches the result of applying all up-migrations.
- [ ] Dashboard's auto-paused badge is visually distinct from the generic paused badge.
- [ ] No placeholder comments left in code (`TODO: audit log`, etc.).
- [ ] No `fmt.Println` or debug `slog.Debug` left in the evaluation path.
