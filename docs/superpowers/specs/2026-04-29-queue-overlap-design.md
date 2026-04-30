**Status:** Proposed
**Date:** 2026-04-29
**Topic:** `overlap_policy: queue` — verify implementation, add regression test, retire TODOs

# Problem

`overlap_policy: queue` is documented as a supported value in the README and PRD
FR-2.2 alongside `skip` and `concurrent`. The runtime implementation is also in
place:

- `internal/scheduler/overlap.go` — `Decide` returns `DecisionQueue` when
  `policy=queue` and there is at least one active run for the same schedule.
- `internal/scheduler/tick.go::processOne` — on `DecisionQueue` it leaves the
  pending row in place and increments `stats.Queued`.
- `internal/scheduler/tick.go::dispatchPending` — its SQL already selects
  `fire_reason='schedule' AND s.overlap_policy='queue' AND NOT EXISTS earlier
  same-schedule pending/running` and dispatches oldest-first.

The two `TODO(P2d)` comments in `overlap.go` and `tick.go` describe a
deliberate design choice (one-tick latency between prior-run-finish and
queued-run-dispatch), not a missing behavior. The real gap is that **no test
exercises the cross-tick handoff end-to-end**, so the behavior is undefended
against future regression.

This spec closes that gap by adding a regression test and retiring the
ambiguous TODO comments.

# Mechanism

No behavioral change. Three concrete changes:

1. **Add a Postgres-backed regression test** in
   `internal/scheduler/tick_test.go` that proves the cross-tick handoff:
   - Insert a schedule with `overlap_policy='queue'`, a 1-minute cron, and
     `next_fire_at` in the past.
   - Insert a synthetic `running` run for that schedule with a `created_at`
     earlier than "now."
   - Call `Tick`. Assert a new pending row exists for the schedule and
     `stats.Queued == 1` — no dispatch happened (the existing running run
     blocks).
   - Mark the prior running run as `succeeded` (terminal).
   - Push `next_fire_at` into the future so the schedule path doesn't
     fire a new row. Call `Tick` again. Assert the queued pending row's
     status is now `running` and `stats.Dispatched == 1`.

2. **Replace the `TODO(P2d)` comments with plain explanatory comments** in
   both `overlap.go` and `tick.go`. The current text is fine modulo dropping
   the TODO prefix — the comment captures real design context (one-tick
   latency, why `dispatchPending` is the catch-up path) and should remain.

3. **Document pause-interaction in code comments.** When auto-pause-on-
   consecutive-failures (commit `a007dfe`) flips `schedule.enabled = false`,
   any queued pending rows for that schedule remain in the table but do
   not dispatch — the `s.enabled = true` clause in `dispatchPending`'s SQL
   already enforces this. When the schedule is re-enabled, the queued rows
   resume draining oldest-first. Add a comment in `dispatchPending`
   explaining that this is intentional. No new code.

# Components

| Path | Change |
|---|---|
| `internal/scheduler/tick_test.go` | Add `TestTick_QueueOverlapDispatchesAfterPriorTerminates` (Postgres-backed; skips under `-short`). |
| `internal/scheduler/overlap.go` | Drop `TODO(P2d)` prefix; keep the explanatory text. |
| `internal/scheduler/tick.go` | Drop `TODO(P2d)` prefix at the `DecisionQueue` branch; keep explanatory text. Add a one-line comment in `dispatchPending` noting the `s.enabled=true` filter is what keeps paused schedules from draining. |

# Tests

The new test uses `testdb.BootPG(t)` and follows the same setup pattern as
existing tests in `tick_test.go` (e.g. `TestTick_DispatchesDue`). It
asserts:

1. After first tick: 2 runs for the schedule (1 running prior + 1 pending
   queued); `stats.Queued == 1`; `stats.Dispatched == 0`.
2. After marking prior succeeded and second tick: queued row's status is
   `running`; `stats.Dispatched == 1`.

If we cannot write this test cleanly against the current schema and
fixtures, that's a signal the dispatch path has hidden coupling and we
should stop and reconsider.

# Operational notes

- Pausing a schedule (auto-pause or manual) leaves any queued rows in place;
  re-enabling resumes drain. This is the existing behavior — the spec only
  documents it.
- Queued depth is unbounded by design. A schedule with a tight cron and a
  slow runner will pile up pending rows. Out of scope here; revisit if it
  bites.
- One-tick latency between "prior run terminates" and "queued run
  dispatches" is intentional. A dedicated drain loop would close that gap;
  not worth the complexity at MVP scale.

# Out of scope

- Per-schedule queued-depth cap.
- A dedicated drain loop that fires immediately on prior-run termination.
- UI surface for queued depth.
- Auto-clearing queued rows on pause.
- Dropping `queue` from the supported policy set.

# Acceptance

1. `go test ./internal/scheduler/...` (with Postgres) passes including the
   new regression test.
2. `go test -short ./...` and `go vet ./...` clean.
3. The string `TODO(P2d)` no longer appears in the repo.
4. README and PRD are unchanged — `queue` remains documented and supported.
