# Auto-Pause on Consecutive Failures — Design

**Status:** Proposed
**Date:** 2026-04-22
**Author:** gambtho (brainstormed with Claude)
**Depends on:** MVP (`docs/superpowers/specs/2026-04-19-cronfoundry-design.md`, deferred item #3)

## Overview

Stop a schedule automatically when its recent scheduled runs are all failing. After *N* consecutive `failed` scheduled runs, the API flips `schedule.enabled=false`, stamps `auto_paused_at` + `auto_pause_reason`, and emits an audit entry + run event. The operator re-enables manually once the underlying issue is fixed.

The feature plugs into existing primitives — the `schedule.enabled` kill-switch, the `run` status lifecycle, `audit_log`, and `run_event` — without a new service or new worker. The goal is crisp: protect against runaway LLM spend and destination noise from a broken skill, without accidentally pausing healthy schedules over flapping webhooks or operator debugging sessions.

## Goals

- Stop runaway LLM spend and destination noise when a skill is actually broken.
- Do not pause healthy schedules over flapping destination webhooks or operator-initiated debugging.
- Reuse `schedule.enabled` — no new state machine, no background sweep process.
- Make an auto-pause observable: dashboard banner, audit entry, run event.

## Non-Goals

- **No automatic resume.** Operators re-enable manually after fixing the root cause.
- **No external notification (Slack/Teams/email) in this spec.** The dashboard banner plus the existing Azure Monitor "consecutive failures" alert rule recommended in the MVP design cover push notification for now. A per-schedule `auto_pause.notify:` field is deferred.
- **No operator-facing global threshold config.** The default (5) is a Go constant; per-schedule YAML overrides are the only user-facing knob.
- **Manual "Run now" failures do not count.** Debug/test runs are expected to fail and must not push a schedule over the edge.
- **`partial_failure` does not count.** Those runs produced LLM output and at least one successful destination; the right fix is usually the broken webhook, not pausing the skill.
- **No time-windowed threshold** ("5 failures in 1h"). Consecutive-failure semantics are what this spec delivers.

## Architecture

Auto-pause is an additive step in the existing finalize path. No new service, no new worker.

### Trigger surface

- The API's `POST /internal/runs/:id/finalize` handler runs an evaluation step **after** its run-row write has committed, in a dedicated short-lived transaction. Keeping evaluation out of the finalize tx ensures a failed audit insert (or any other evaluation-step error) can never poison the tx and roll back the load-bearing run-row write.
- Only a run with `fire_reason='schedule'` AND terminal `status='failed'` triggers evaluation.
- Evaluation queries the last *N* terminal scheduled runs for this schedule with `created_at >= schedule.last_enabled_at`, ordered by `created_at DESC, id DESC`. (The query uses `created_at`, not `started_at`, because `started_at` is nullable — runs that failed before dispatch have `started_at IS NULL` and must still count toward the streak.) If the query returns *N* rows and every row is `failed`, the schedule is auto-paused atomically within the evaluation transaction.

### State

Four new columns on `schedule`:

| Column | Type | Semantics |
| --- | --- | --- |
| `auto_pause_after` | `int NULL` | Per-schedule threshold override from `cronfoundry.yaml`. `NULL` = use global default. |
| `auto_paused_at` | `timestamptz NULL` | When the most recent auto-pause fired. `NULL` = not currently auto-paused. |
| `auto_pause_reason` | `text NULL` | Human-readable reason, e.g. `"5 consecutive failed runs"`. `NULL` = not currently auto-paused. |
| `last_enabled_at` | `timestamptz NOT NULL DEFAULT now()` | Anti-flap boundary. Bumped whenever `enabled` transitions `false → true`. Consecutive-failure check only considers runs with `created_at >= last_enabled_at`. |

Global default: `DefaultAutoPauseAfter = 5` — a package-level constant in `internal/api`, co-located with the finalize handler that reads it.

### Resume

The existing pause/resume handler extends to, atomically on `false → true`:

1. Set `enabled = true`.
2. Clear `auto_paused_at` and `auto_pause_reason`.
3. Set `last_enabled_at = now()`.

A single `UPDATE` statement. Audit-logged as the existing `schedule.enabled` action.

## Components

### Database migration

One migration, `internal/db/migrations/20260422000001_auto_pause.sql`:

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

No new indexes. The trigger query is bounded by the threshold (~5 rows) and scoped by `schedule_id`, which is already covered by `run_schedule_created_idx`.

Existing rows backfill `last_enabled_at = now()` via the `DEFAULT` clause on apply — no explicit backfill script.

### Config parsing

`internal/config/manifest.go` (and its JSON Schema) adds a schedule-level `auto_pause` object:

```yaml
schedules:
  - name: daily-research-agent
    cron: "0 9 * * *"
    provider: anthropic
    model: claude-opus-4-7
    auto_pause:
      after: 3
```

- `auto_pause.after`: integer, `minimum: 1`. Missing → schedule stores `auto_pause_after=NULL`, runtime falls back to `DefaultAutoPauseAfter`.
- Object shape (not a scalar) reserves the namespace for future fields without a YAML-breaking change.
- Validation rejects `after: 0`, negatives, and non-integer values at sync time.

The sync path already writes schedule rows on `cronfoundry.yaml` changes; the parsed `auto_pause_after` flows through the same upsert.

### API finalize handler

New file `internal/api/finalize_autopause.go` — keeps the existing finalize handler readable and gives the logic an obvious home for test coverage. (The run-finalize HTTP handler lives in `internal/api`, not `internal/webapi` — `webapi` hosts user-facing `/api/*` routes; `internal/api` hosts the runner-facing `/internal/*` routes that this feature plugs into.)

Exported function:

```go
// evaluateAutoPause is called from the run-finalize handler after the
// terminal run-row write has committed. It opens its own short-lived
// transaction so an evaluation error cannot roll back the run-row write.
// No-ops unless the finalized run is a failed scheduled run, and even
// then only acts if the full consecutive-failure condition is met.
func evaluateAutoPause(
    ctx context.Context,
    pool *pgxpool.Pool,
    scheduleID uuid.UUID,
    runID uuid.UUID,
    runStatus string,
    fireReason string,
) error
```

Behavior:

1. Return nil if `fireReason != "schedule"` or `runStatus != "failed"`.
2. Open a transaction on `pool`. All subsequent steps execute inside it.
3. Load the schedule's `auto_pause_after` and `last_enabled_at`. Resolve threshold as `auto_pause_after` if non-null else `DefaultAutoPauseAfter`.
4. Query:
   ```sql
   SELECT status FROM run
    WHERE schedule_id = $1
      AND fire_reason = 'schedule'
      AND status IN ('succeeded','partial_failure','failed')
      AND created_at >= $2   -- schedule.last_enabled_at
    ORDER BY created_at DESC, id DESC
    LIMIT $3                 -- threshold
   ```
5. If the result has fewer than `threshold` rows, rollback and return nil.
6. If any row's status is not `failed`, rollback and return nil.
7. Conditional update (idempotent under race):
   ```sql
   UPDATE schedule
      SET enabled=false,
          auto_paused_at=now(),
          auto_pause_reason=$1   -- "<threshold> consecutive failed runs"
    WHERE id=$2 AND enabled=true
   ```
8. If `UPDATE` affected 0 rows (race: another finalize already paused it), rollback and return nil — do not insert duplicate audit or run-event rows.
9. Insert `audit_log` row `(action='schedule.auto_paused', target=<scheduleID>, detail_json={threshold, last_run_id})`.
10. Insert `run_event` row `(run_id=<runID>, event_type='schedule.auto_paused', payload_json={threshold})`.
11. Commit.

**Failure mode:** the run-finalize handler calls `evaluateAutoPause` after its own tx commits, and logs-and-swallows any error. The run-row write is load-bearing and is already durable by that point; the evaluation transaction is fully independent, so an insert failure here rolls back only the UPDATE + audit + run_event and is self-correcting (the next failure re-evaluates and will re-trigger).

### Schedule resume handler

`internal/webapi/schedules.go` (or wherever pause/resume lives). On the `enabled=true` path:

```sql
UPDATE schedule
   SET enabled=true,
       auto_paused_at=NULL,
       auto_pause_reason=NULL,
       last_enabled_at=now()
 WHERE id=$1
```

Single statement, no read-modify-write. `last_enabled_at` is bumped unconditionally on the handler's resume path — safe because the handler only runs on an explicit resume action.

Existing audit entry (`schedule.enabled`) continues to fire.

### UI

`web/src/components/ScheduleBadge.tsx` (or equivalent):
- New amber "Auto-paused" badge variant alongside the existing gray "Paused" badge.
- Tooltip shows `auto_pause_reason` and relative time from `auto_paused_at` (e.g. "2h ago").

Schedule detail page:
- When `auto_paused_at` is non-null, render a top-of-page banner:
  > ⚠️ Auto-paused 2h ago — 5 consecutive failed runs. Resume once the underlying issue is fixed.
- The existing Resume button remains the only action.

Run detail page:
- If this run has a `run_event` of type `schedule.auto_paused`, render an inline note on that row: *"This failure triggered auto-pause."*

No new routes, no new API endpoints. `GET /schedules`, `GET /schedules/:id`, and `GET /runs/:id/events` already carry everything; the new `schedule` columns pass through existing response shapes.

## Data flow

### 1. Auto-pause triggers (5 of 5 failures)

```text
Runner → POST /internal/runs/:id/finalize (status=failed)
  → API handler:
     finalizeTx: BEGIN; UPDATE run SET status='failed', ...; COMMIT
     evaluateAutoPause(pool, ...):
       pauseTx: BEGIN
         SELECT ... → [failed, failed, failed, failed, failed]
         UPDATE schedule SET enabled=false, auto_paused_at=now(),
                             auto_pause_reason='5 consecutive failed runs'
           WHERE id=:sid AND enabled=true    -- 1 row affected
         INSERT INTO audit_log (...'schedule.auto_paused'...)
         INSERT INTO run_event (...'schedule.auto_paused'...)
       COMMIT
```

### 2. Streak broken (success in window)

```text
pauseTx: BEGIN
  SELECT ... → [failed, failed, succeeded, failed, failed]
  → any(status != 'failed') → ROLLBACK, return nil
```

### 3. Race (two finalizes at once)

```text
Both finalize their run rows independently (separate tx's, both commit).
Each then opens its own pauseTx:
  Tx1 SELECT → 5 failed. UPDATE WHERE enabled=true → 1 row. INSERTs. COMMIT.
  Tx2 SELECT → 5 failed. UPDATE WHERE enabled=true → 0 rows.
       ROLLBACK, return nil. No duplicate audit or run_event.
```

### 4. Resume

```text
User clicks Resume → POST /schedules/:id/resume
  → API handler:
     UPDATE schedule SET enabled=true, auto_paused_at=NULL,
                         auto_pause_reason=NULL, last_enabled_at=now()
       WHERE id=:sid
     INSERT INTO audit_log (...'schedule.enabled'...)
```

### 5. Post-resume fresh window

```text
A new scheduled fire fails. evaluateAutoPause runs:
  SELECT ... WHERE created_at >= schedule.last_enabled_at LIMIT 5
  → only 1 row (the just-finalized one) — count < threshold → return nil
No pause. Pre-resume failures are correctly excluded.
```

## Error handling

| Failure | Behavior |
| --- | --- |
| `evaluateAutoPause` SELECT, UPDATE, or INSERT errors | `pauseTx` rolls back. Caller logs and swallows. Run-row write is already committed in its own tx, so it is not affected. The next scheduled failure re-evaluates and will re-trigger the pause if the streak still holds. |
| Schedule already paused (manual or prior auto-pause) | `UPDATE ... WHERE enabled=true` matches 0 rows. `pauseTx` rolls back before inserts. No duplicate audit_log, no duplicate run_event. |
| Threshold from YAML is invalid (e.g. operator edits DB directly to a negative value) | `auto_pause_after` treated as if NULL; fall back to `DefaultAutoPauseAfter`. |
| `pool` unavailable at evaluation time | Caller logs and swallows. Run finalize response is unaffected. |

## Testing

### Unit — `internal/webapi/finalize_autopause_test.go` (new)

- **Trigger:** threshold=5; seed 4 failed + 1 new-failed scheduled runs → pauses, stamps reason, writes audit + run_event rows.
- **Streak broken by success:** 4 failed + 1 succeeded + 1 new-failed → no pause.
- **Streak broken by partial_failure:** 4 failed + 1 partial_failure + 1 new-failed → no pause.
- **Manual excluded:** 4 failed scheduled + 1 new failed manual → no pause.
- **Anti-flap window:** 5 failed runs, one with `created_at < last_enabled_at` → no pause (only 4 in-window).
- **Per-schedule override:** `auto_pause_after=3`; 3 failed → pauses at 3.
- **Idempotent pause:** already-paused schedule, new failure → UPDATE no-op, no duplicate rows.
- **Insufficient history:** threshold=5, only 3 runs so far, all failed → no pause.

### Unit — `internal/config/manifest_test.go` (extended)

- `auto_pause.after: 0` → schema rejection.
- `auto_pause.after: -1` → schema rejection.
- `auto_pause.after: "five"` → schema rejection.
- Missing `auto_pause` → parses with `AutoPauseAfter == nil`.
- `auto_pause.after: 3` → parses with `*AutoPauseAfter == 3`.

### Integration — `internal/scheduler` or new e2e

- Full scheduler → runner → finalize loop driven with a provider that always errors. Confirm `schedule.enabled` flips to false after `threshold` ticks and no further runs are dispatched on subsequent ticks.
- Resume via `POST /schedules/:id/resume`; confirm `last_enabled_at` bumps, `auto_paused_at`/`auto_pause_reason` clear, `audit_log` has both pause and resume rows.

### Migration

- Apply + roll back locally; verify schema matches pre-migration state after down.
- Pre-existing schedules get `last_enabled_at = now()` via the `DEFAULT` clause on apply — no separate backfill.

## Rollout

- Single PR: migration + config schema + finalize handler + resume handler + UI.
- On deploy: all existing schedules start counting fresh. No operator action required, no feature flag.
- Behavior is purely additive and conservative by default (threshold 5, strict-`failed`-only, scheduled-only).

## Deferred / Future

Explicitly out of scope; revisit after this ships:

1. **Per-schedule `auto_pause.notify:`** — reuse destination adapters to push "X auto-paused" to Slack/Teams.
2. **Auto-resume** after a cool-down window or a successful manual run.
3. **Time-windowed threshold** ("5 failures in 1h" rather than strict-consecutive).
4. **Partial-failure rate trigger** as a separate signal (distinct from hard-failure auto-pause).
5. **Operator-facing global threshold config** (env var or admin UI) if the constant proves unfit.
