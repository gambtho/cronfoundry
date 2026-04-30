# Item #5: Implement or remove the `queue` overlap policy

## Background

`internal/scheduler/overlap.go:30` and `internal/scheduler/tick.go:172` both
contain a `TODO(P2d)` acknowledging that schedules with `overlap_policy: queue`
have queued runs created as `pending` rows but **never picked up** when the
prior run terminates.

The README and PRD FR-2.2 both list `queue` as a supported overlap policy
alongside `skip` and `concurrent`. A user setting `overlap_policy: queue` on
a tight schedule today will silently pile up `pending` rows that never
execute. This is the one user-facing config knob whose stated behavior
diverges from runtime.

This is the most ambiguous remaining item — it could be solved by
implementing the missing dispatch, or by dropping `queue` from the supported
set and rejecting it at config-validation time. Both have merit.

## Goal

Make the scheduler's behavior match the documented config surface — either
by implementing `queue` correctly, or by removing it cleanly with a clear
config-validation error.

## How to start

1. Open this worktree:
   ```bash
   cd /home/tng/workspace/cronfoundry
   git worktree add .claude/worktrees/spec-queue-overlap -b worktree-spec-queue-overlap main
   cd .claude/worktrees/spec-queue-overlap
   ```
2. Read `00-context.md` (in this same directory) for project conventions.
3. Read the relevant code:
   - `internal/scheduler/overlap.go` (the `Decide` function and `Policy` type)
   - `internal/scheduler/tick.go` (the dispatch loop, esp. `processOne` and
     `dispatchPending` around lines 100-360)
   - `internal/config/manifest.go` (manifest validation — find where
     `overlap_policy` is parsed/validated)
4. Run the brainstorming skill — this needs a brainstorm before writing
   code:
   ```text
   superpowers:brainstorming
   ```

## Brainstorm questions to answer

Pick a direction with the user via `AskUserQuestion`:

1. **Implement vs. drop?** Two options:
   - **Implement** — modify `dispatchPending` (already iterates oldest-first
     over pending rows for `queue` schedules) so it correctly skips a
     schedule's queued rows when an earlier same-schedule run is still
     active. The SQL already has the `NOT EXISTS` clause that protects
     this. Verify and add tests. Smaller diff than expected — the missing
     piece may just be a test, not implementation.
   - **Drop** — at manifest parse time, reject `overlap_policy: queue` with a
     clear error. Update README + PRD FR-2.2 to remove `queue`. ~30 min of
     work; user gets a clear failure rather than silent backlog.
2. **If implementing — depth limit?** A pathological case: a schedule fires
   every 5 minutes but each run takes 30 minutes. Without a limit, queued
   rows accumulate. Cap depth (e.g. 5 queued, drop older ones) or trust the
   operator?
3. **If implementing — should auto-pause-on-consecutive-failures
   (already shipped, commit `a007dfe`) interact with `queue`?** Currently a
   paused schedule's queued rows would just sit there. Document or auto-clean?

## What to deliver

Standard flow:

1. Spec → `docs/superpowers/specs/2026-04-29-queue-overlap-design.md`
2. Plan → `docs/superpowers/plans/2026-04-29-queue-overlap.md`
3. Implementation in `worktree-spec-queue-overlap`
4. PR with title like `feat(scheduler): implement queue overlap policy` or
   `fix(scheduler): reject queue overlap policy at parse time`

Include in the PR body: explicit confirmation that the docs and code now
agree, with a pointer to the specific README/PRD lines that changed (if any).

## Test signals to look for

A clean answer will have a test that:

- Creates a schedule with `overlap_policy: queue` and a 1-minute cron.
- Inserts a "running" run for it that doesn't terminate.
- Fires the next tick — the second pending row is created (existing
  behavior).
- Terminates the running run.
- Fires another tick — verifies the queued row is now dispatched (the
  currently-broken behavior). Or, if dropping, verifies the manifest is
  rejected at parse time.

If you can't write that test cleanly, the design is probably wrong.

## Out of scope

- Reworking the scheduler tick loop architecturally.
- Adding a new policy type.
- Per-schedule queue depth UI.

## Acceptance

1. Either: a `queue` schedule with a long-running prior run dispatches its
   queued row when the prior terminates AND a regression test guards it; OR:
   the manifest parser rejects `overlap_policy: queue` with a clear error
   AND the README/PRD no longer document it.
2. The two `TODO(P2d)` comments in `overlap.go` and `tick.go` are removed
   (whichever path is taken).
3. `go test -short ./...` and `go test ./...` (with Postgres) green.
4. `go vet ./...` clean.
