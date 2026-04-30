# Queue Overlap Policy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Postgres-backed regression test that proves `overlap_policy: queue` dispatches a queued pending row on the next tick after the prior run terminates, then retire the two `TODO(P2d)` markers.

**Architecture:** No behavior change — the runtime already implements `queue` correctly via `Decide → DecisionQueue` (leaves pending row in place) and `dispatchPending` (oldest-first SQL filter that ignores rows blocked by an earlier same-schedule pending/running). The plan adds a single integration test that exercises the full cross-tick handoff and rewrites the two `TODO(P2d)` comments into plain explanatory comments.

**Tech Stack:** Go 1.25, `pgx/v5`, `stretchr/testify`, `internal/testdb` (testcontainers Postgres).

---

## File Map

| Path | Change |
|---|---|
| `internal/scheduler/tick_test.go` | **Modify** — add `TestTick_QueueOverlapDispatchesAfterPriorTerminates`. |
| `internal/scheduler/overlap.go` | **Modify** — drop `TODO(P2d):` prefix from the doc comment on `Decide`; keep the explanatory text. |
| `internal/scheduler/tick.go` | **Modify** — drop `TODO(P2d)` reference from the `DecisionQueue` branch comment in `processOne`; add a one-line comment on `dispatchPending`'s `s.enabled = true` clause noting that paused schedules retain queued rows by design. |

---

### Task 1: Regression test for queue overlap cross-tick handoff

**Files:**
- Modify: `internal/scheduler/tick_test.go` (append the new test at end of file)

The implementation under test already exists; the test should pass as-is. We still write it test-first so we observe a real failure if the implementation regresses.

- [ ] **Step 1: Write the failing test**

Append to `internal/scheduler/tick_test.go`:

```go
// TestTick_QueueOverlapDispatchesAfterPriorTerminates pins the cross-tick
// handoff for overlap_policy=queue: when a queue-policy schedule fires while
// a prior run is still active, the new pending row is left in place
// (stats.Queued++); on a subsequent tick after the prior run terminates,
// dispatchPending picks the queued row up and dispatches it.
func TestTick_QueueOverlapDispatchesAfterPriorTerminates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	pool, cleanup := testdb.BootPG(t)
	defer cleanup()

	ctx := context.Background()
	schedID := seedDueSchedule(t, pool, "queue")

	// Seed a prior 'running' run for this schedule. Its created_at is in the
	// past so dispatchPending's "earlier same-schedule pending/running"
	// guard treats it as the blocker for any queued row inserted by Tick.
	var orgID pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT org_id FROM schedule WHERE id = $1`, schedID).Scan(&orgID))
	var priorID pgtype.UUID
	priorCreated := time.Now().Add(-2 * time.Minute)
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO run (org_id, schedule_id, skill_sha, status, fire_reason,
			runner_token_hash, started_at, created_at)
		 VALUES ($1, $2, 'sha-prior', 'running', 'schedule', 'hash-prior', $3, $3)
		 RETURNING id`, orgID, schedID, priorCreated).Scan(&priorID))

	mock := &mockDispatcher{}
	deps := Deps{
		Pool:         pool,
		Signer:       newSigner(t),
		Dispatcher:   mock,
		APIBaseURL:   "http://127.0.0.1:8080",
		RunnerBinary: "/usr/bin/true",
	}

	// First tick: schedule is due, processOne inserts a pending row, Decide
	// returns DecisionQueue (prior 'running' run blocks dispatch), pending
	// row stays. dispatchPending's NOT EXISTS clause keeps it queued because
	// the prior running row is older.
	stats, err := Tick(ctx, deps)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Dispatched, "first tick must not dispatch the queued row")
	assert.Equal(t, 1, stats.Queued, "first tick must record one Queued decision")

	mock.mu.Lock()
	assert.Empty(t, mock.calls, "no dispatch should happen while prior is running")
	mock.mu.Unlock()

	var pendingCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM run WHERE schedule_id = $1 AND status='pending'`,
		schedID).Scan(&pendingCount))
	assert.Equal(t, 1, pendingCount, "exactly one queued pending row should exist")

	// Terminate the prior run.
	_, err = pool.Exec(ctx,
		`UPDATE run SET status='succeeded', finished_at=now() WHERE id=$1`, priorID)
	require.NoError(t, err)

	// Push schedule's next_fire_at into the future so processOne does not
	// fire a new run on the second tick. We're isolating dispatchPending's
	// drain behavior.
	_, err = pool.Exec(ctx,
		`UPDATE schedule SET next_fire_at = now() + interval '1 hour' WHERE id=$1`,
		schedID)
	require.NoError(t, err)

	// Second tick: dispatchPending should pick up the previously-queued
	// pending row.
	stats, err = Tick(ctx, deps)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Dispatched, "second tick must dispatch the queued row")

	mock.mu.Lock()
	require.Len(t, mock.calls, 1, "exactly one dispatch on second tick")
	mock.mu.Unlock()

	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM run WHERE schedule_id=$1 AND status<>'succeeded'`,
		schedID).Scan(&status))
	assert.Equal(t, "running", status, "queued row should now be running")
}
```

- [ ] **Step 2: Run the test to verify it passes against the existing implementation**

Run:
```bash
go test ./internal/scheduler/ -run TestTick_QueueOverlapDispatchesAfterPriorTerminates -v
```
Expected: `PASS`. (The implementation is already in place; this test is a regression guard. If it fails, stop and inspect — the implementation has drifted from the spec.)

- [ ] **Step 3: Sanity-check by temporarily breaking the implementation**

Open `internal/scheduler/tick.go`, temporarily change the `DecisionQueue` branch in `processOne` to `return q.DeleteRun(ctx, run.ID)` (i.e. behave like skip). Re-run:
```bash
go test ./internal/scheduler/ -run TestTick_QueueOverlapDispatchesAfterPriorTerminates -v
```
Expected: `FAIL` (queued row deleted; second-tick dispatch never happens). Revert the temporary change. Re-run; expected: `PASS`.

- [ ] **Step 4: Run the full scheduler test package**

```bash
go test ./internal/scheduler/ -v
```
Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/tick_test.go
git commit -m "test(scheduler): regression test for queue overlap cross-tick dispatch"
```

---

### Task 2: Retire the `TODO(P2d)` markers and clarify pause interaction

**Files:**
- Modify: `internal/scheduler/overlap.go:30-36` — replace the `TODO(P2d)` doc paragraph with a non-TODO explanatory paragraph.
- Modify: `internal/scheduler/tick.go:169-174` — replace the `DecisionQueue` branch comment so it no longer references `TODO(P2d) in overlap.go`.
- Modify: `internal/scheduler/tick.go:301-303` — add a one-line comment on the `s.enabled = true` clause explaining the paused-schedule retention.

- [ ] **Step 1: Edit `internal/scheduler/overlap.go`**

Replace this comment block above `func Decide`:

```go
// TODO(P2d): queued runs are left pending by processOne and then picked up
// by tick.dispatchPending on a subsequent tick once prior runs finish. The
// two paths are split deliberately: Tick's primary loop only walks schedules
// whose next_fire_at is due, so a run that was queued (and has no further
// scheduled fire until its cron boundary) would otherwise never get picked
// up. The trade-off is a one-tick latency between prior-run-finish and
// queued-run-dispatch; a dedicated queue-drain loop would close that gap.
```

with:

```go
// Queued runs are left pending by processOne and picked up by
// tick.dispatchPending on a subsequent tick once prior runs finish. The
// split is deliberate: Tick's primary loop only walks schedules whose
// next_fire_at is due, so a run that was queued (and has no further
// scheduled fire until its cron boundary) would otherwise never get picked
// up. The trade-off is a one-tick latency between prior-run-finish and
// queued-run-dispatch; a dedicated queue-drain loop would close that gap
// but is not worth the complexity at MVP scale.
```

- [ ] **Step 2: Edit `internal/scheduler/tick.go` (DecisionQueue branch)**

Replace the comment inside the `case DecisionQueue:` arm in `processOne`:

```go
		// Leave the pending row in place. dispatchPending (invoked at the
		// end of Tick, and on every subsequent tick) will pick it up once
		// the prior run terminates. See TODO(P2d) in overlap.go.
```

with:

```go
		// Leave the pending row in place. dispatchPending (invoked at the
		// end of Tick, and on every subsequent tick) will pick it up once
		// the prior run terminates. See the doc comment on Decide for why
		// the queue drain lives there instead of in this loop.
```

- [ ] **Step 3: Edit `internal/scheduler/tick.go` (dispatchPending SQL — pause comment)**

In the `dispatchPending` SQL string, immediately above the line `AND s.enabled = true`, add a comment line. The current block:

```go
		WHERE r.status = 'pending'
		  AND s.enabled = true
		  AND (
```

becomes:

```go
		WHERE r.status = 'pending'
		  -- s.enabled = true: paused schedules (auto-paused on consecutive
		  -- failures, or manually disabled) retain their queued rows but do
		  -- not drain. Re-enabling resumes drain oldest-first.
		  AND s.enabled = true
		  AND (
```

(SQL `--` line comments are accepted by Postgres inside a multi-line string.)

- [ ] **Step 4: Verify no `TODO(P2d)` markers remain**

```bash
grep -rn "TODO(P2d)" internal/
```
Expected: no output.

- [ ] **Step 5: Run the full test suite + vet**

```bash
go vet ./...
go test ./internal/scheduler/ -v
```
Expected: clean vet, all tests pass (especially `TestTick_QueueOverlapDispatchesAfterPriorTerminates` — the SQL comment must not break the query).

- [ ] **Step 6: Commit**

```bash
git add internal/scheduler/overlap.go internal/scheduler/tick.go
git commit -m "refactor(scheduler): retire TODO(P2d) — queue overlap is implemented"
```

---

### Task 3: Final verification

- [ ] **Step 1: Run short and full test suites**

```bash
go test -short ./...
go test ./...
go vet ./...
```
Expected: all green.

- [ ] **Step 2: Confirm no spec drift**

```bash
grep -n "queue" README.md docs/superpowers/specs/2026-04-19-cronfoundry-prd.md | head
```
Expected: existing mentions of `queue` overlap policy unchanged. (No edits intended.)

- [ ] **Step 3: Push branch and open PR (handled in next phase, not in this task)**

(Pushed manually outside the plan.)

---

## Self-review

- **Spec coverage:**
  - Mechanism #1 (regression test) → Task 1.
  - Mechanism #2 (drop TODO(P2d) prefix in both files) → Task 2 steps 1–2.
  - Mechanism #3 (pause-interaction comment) → Task 2 step 3.
  - Acceptance #1 (full Postgres test passes) → Task 1 step 4 + Task 3 step 1.
  - Acceptance #2 (-short and vet clean) → Task 3 step 1.
  - Acceptance #3 (no `TODO(P2d)`) → Task 2 step 4.
  - Acceptance #4 (README/PRD untouched) → Task 3 step 2.
- **Placeholders:** None.
- **Type consistency:** Test reuses existing `seedDueSchedule`, `mockDispatcher`, `newSigner`, `Deps` — all match `tick_test.go` patterns. `pgtype.UUID` and `pgtype.Timestamptz` usage matches existing tests.
