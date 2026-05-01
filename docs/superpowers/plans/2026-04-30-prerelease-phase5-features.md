# Pre-release Polish — Phase 5: Operator Features (Verification + Polish)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface the three operator features that are already shipped on the backend (auto-pause, manual run replay, token/cost) in a polished UI consistent with Phase 3, and verify each end-to-end.

**Architecture:** Most of the original Phase 5 scope shipped before this plan was written:

| Feature | Backend | UI today | Phase 5 work |
|---------|---------|----------|--------------|
| 5a Auto-pause on consecutive failures | Shipped (PR #17): `auto_pause_after`, `auto_paused_at`, `auto_pause_reason`, evaluator | Surfaced on Dashboard (commit `0a47060`) | Verify; add Resume button + manifest reference; e2e test |
| 5b Manual run replay | Shipped: `/internal/schedules/{id}/run-now`, webapi proxy in `internal/webapi/schedules.go` | None | Add "Run now" button; toast on 409 already-running; audit verify |
| 5c Token/cost surfacing | Shipped: `Usage` parsing, `pricing.CostCents`, `tokens_in`/`tokens_out`/`cost_cents` columns, runner→finalize wiring, basic display in `Runs.tsx` | Plain text in Runs page | Polish: dedicated columns in Phase-3 DataTable, Dashboard tile, run-detail emphasis, tooltip explaining "estimated" |

**Tech Stack:** Go 1.25 (verification only), React 18, Phase-3 shadcn primitives, vitest, testify.

**Spec:** `docs/superpowers/specs/2026-04-30-prerelease-polish-design.md` §Phase 5.

**Prerequisite:** Phase 3 (shadcn primitives, DataTable, DropdownMenu, Toast) is complete.

---

## Task 1: Verify auto-pause works end-to-end against deployed Azure instance

**Files:**
- Read-only: `internal/api/finalize.go`, `internal/scheduler/tick.go`, `internal/sync/upsert.go`, `internal/db/queries/schedule.sql`
- Test: new e2e or manual procedure.

This is verification, not new code.

- [ ] **Step 1: Read the auto-pause evaluator** — confirm the evaluator runs in finalize and the scheduler skips paused schedules.

```bash
grep -n "evaluateAutoPause\|auto_paused_at" internal/api/finalize.go internal/scheduler/tick.go
```

Expected: `evaluateAutoPause` is called from `Finalize`; `tick.go` skips schedules with `auto_paused_at IS NOT NULL` (or via `enabled=false`, depending on implementation).

- [ ] **Step 2: Add an e2e test if absent**

```bash
grep -rln "auto.pause\|AutoPause" cmd/cronfoundry/e2e_test.go
```

If no e2e covers the path, add one:

```go
// cmd/cronfoundry/e2e_test.go (within e2e tag)
func TestE2E_AutoPauseAfterThreeFailures(t *testing.T) {
	// boot stack with a schedule that has auto_pause_after: 3
	// inject 3 failed runs (use the LLM stub that always errors)
	// wait for finalize to complete
	// assert schedule.auto_paused_at IS NOT NULL
	// assert next tick does not dispatch a new run
}
```

Write the test such that the run-trigger / finalize / tick path exercises everything.

- [ ] **Step 3: Run e2e**

Run: `make e2e`
Expected: PASS.

- [ ] **Step 4: Manual UI verification on a deployed instance**

After Phase 2 dogfood, on a live Azure deploy: configure a schedule with `auto_pause_after: 3` and an intentionally-broken provider (wrong model name). Wait. Confirm:
- Three failed runs land
- Schedule shows the paused state on the dashboard
- Next tick does not dispatch
- Audit log shows the auto-pause event

- [ ] **Step 5: Commit (only if e2e was added)**

```bash
git add cmd/cronfoundry/e2e_test.go
git commit -m "test(e2e): auto-pause triggers after 3 consecutive failures"
```

---

## Task 2: Add explicit "Resume" button on paused schedule rows

**Files:**
- Modify: `web/src/pages/Schedules.tsx` (or `Dashboard.tsx` if schedules surface there)
- Modify: `web/src/lib/api.ts` to add `resumeSchedule(id)`
- Verify: webapi endpoint exists.

- [ ] **Step 1: Verify endpoint**

```bash
grep -n "resume\|enabled.*true\|/api/schedules.*PATCH" internal/webapi/schedules.go
```

Expected: a PATCH or POST endpoint that clears `auto_paused_at` and re-enables the schedule. Per commit `c4fe277` it exists. Note its exact path.

- [ ] **Step 2: Add an api.ts call**

```ts
// web/src/lib/api.ts
export async function resumeSchedule(id: string): Promise<void> {
  const csrf = getCSRFToken()
  const r = await fetch(`/api/schedules/${id}/resume`, {  // confirm path
    method: 'POST',
    headers: { 'X-CSRF-Token': csrf },
    credentials: 'same-origin',
  })
  if (!r.ok) throw new Error(`resume failed: ${r.status}`)
}
```

- [ ] **Step 3: Add a "Resume" button on paused rows**

In Schedules row rendering (or wherever paused schedules show), if `schedule.auto_paused_at` is set:

```tsx
import { Button } from '@/components/ui/button'
import { useToast } from '@/lib/use-toast'
import { resumeSchedule } from '@/lib/api'
import { useQueryClient } from '@tanstack/react-query'

// inside row renderer:
const qc = useQueryClient()
const { toast } = useToast()
{schedule.auto_paused_at && (
  <Button
    size="sm"
    variant="outline"
    onClick={async () => {
      try {
        await resumeSchedule(schedule.id)
        qc.invalidateQueries({ queryKey: ['schedules'] })
        toast({ title: 'Schedule resumed', description: schedule.name })
      } catch (e) {
        toast({ title: 'Resume failed', description: String(e), variant: 'destructive' })
      }
    }}
  >
    Resume
  </Button>
)}
```

- [ ] **Step 4: Test**

```tsx
// in the schedules page test
it('renders Resume button on paused schedule', async () => {
  // stub fetch to return a paused schedule
  // render Schedules
  // expect getByRole('button', { name: /resume/i })
})
```

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(ui): Resume button for auto-paused schedules"
```

---

## Task 3: Add "Run now" button to schedule rows (replay)

**Files:**
- Modify: `web/src/pages/Schedules.tsx` — add Run-now button.
- Modify: `web/src/lib/api.ts` — add `triggerRunNow(id)` if missing.
- Verify: `/api/schedules/{id}/run-now` returns the new run ID; UI redirects to RunDetailSheet.

- [ ] **Step 1: Verify API behavior**

```bash
grep -n "run-now\|RunNow\|\"id\".*string" internal/webapi/schedules.go
```

Confirm:
- POST to `/api/schedules/{id}/run-now` returns `200` with `{run_id: "..."}`
- Returns `409` when `overlap_policy: skip` blocks it
- Audit event `schedule.run_now` is emitted (per commit history)

If response shape isn't documented, write down what it actually returns.

- [ ] **Step 2: Add api.ts function**

```ts
// web/src/lib/api.ts
export async function triggerRunNow(scheduleId: string): Promise<{ run_id: string }> {
  const r = await fetch(`/api/schedules/${scheduleId}/run-now`, {
    method: 'POST',
    headers: { 'X-CSRF-Token': getCSRFToken() },
    credentials: 'same-origin',
  })
  if (r.status === 409) {
    const body = await r.json().catch(() => ({}))
    const err = new Error(body.message ?? 'already running')
    ;(err as Error & { code?: string }).code = 'already_running'
    throw err
  }
  if (!r.ok) throw new Error(`run-now failed: ${r.status}`)
  return r.json()
}
```

- [ ] **Step 3: Add Run-now button + 409 handling**

```tsx
{!schedule.auto_paused_at && (
  <Button
    size="sm"
    variant="outline"
    onClick={async () => {
      try {
        const { run_id } = await triggerRunNow(schedule.id)
        navigate(`/runs?id=${run_id}`)  // or open RunDetailSheet directly
      } catch (e) {
        const code = (e as { code?: string }).code
        if (code === 'already_running') {
          toast({ title: 'Already running', description: 'A run is already in flight for this schedule.' })
        } else {
          toast({ title: 'Run failed to start', description: String(e), variant: 'destructive' })
        }
      }
    }}
  >
    Run now
  </Button>
)}
```

- [ ] **Step 4: Test**

```tsx
it('triggers run on Run now click', async () => {
  global.fetch = vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    json: async () => ({ run_id: 'r-123' }),
  } as Response)
  // render Schedules; click "Run now"; assert navigate called with /runs?id=r-123
})

it('toasts on 409 already-running', async () => {
  global.fetch = vi.fn().mockResolvedValue({
    ok: false,
    status: 409,
    json: async () => ({ message: 'already running' }),
  } as Response)
  // render; click; assert toast appears
})
```

- [ ] **Step 5: e2e**

If absent, add to `cmd/cronfoundry/e2e_test.go`:

```go
func TestE2E_RunNowDispatches(t *testing.T) {
	// POST /api/schedules/<id>/run-now
	// expect 200 + run_id; expect run row exists with status pending/running
	// expect audit event "schedule.run_now"
}
```

Run: `make e2e`. PASS.

- [ ] **Step 6: Commit**

```bash
git commit -m "feat(ui): Run-now button on schedule rows; 409 toast"
```

---

## Task 4: Promote tokens & cost into the Runs DataTable

Phase 3 Task 11 introduced `DataTable`. Here we add dedicated columns rather than the inline text in the current `Runs.tsx`.

**Files:**
- Modify: `web/src/pages/Runs.tsx` — restructure into columns: Status, Schedule, Provider/Model, Started, Duration, Tokens, Cost.
- Test: vitest.

- [ ] **Step 1: Update column definitions**

```tsx
const columns: ColumnDef<Run>[] = [
  { accessorKey: 'status', header: 'Status', cell: ({ row }) => <RunStatusBadge status={row.original.status} /> },
  { accessorKey: 'schedule_name', header: 'Schedule' },
  {
    id: 'model',
    header: 'Model',
    cell: ({ row }) => <span className="text-muted-foreground text-xs">{row.original.llm_provider}/{row.original.llm_model}</span>,
  },
  { accessorKey: 'started_at', header: 'Started', cell: ({ row }) => formatRelative(row.original.started_at) },
  { accessorKey: 'duration_ms', header: 'Duration', cell: ({ row }) => row.original.duration_ms ? `${(row.original.duration_ms / 1000).toFixed(1)}s` : '—' },
  {
    id: 'tokens',
    header: 'Tokens',
    cell: ({ row }) => {
      const r = row.original
      if (r.tokens_in == null) return <span className="text-muted-foreground">—</span>
      return <span>{r.tokens_in.toLocaleString()} / {r.tokens_out?.toLocaleString() ?? '—'}</span>
    },
  },
  {
    id: 'cost',
    header: 'Cost',
    cell: ({ row }) => {
      const c = row.original.cost_cents
      if (c == null) return <span className="text-muted-foreground">—</span>
      return (
        <Tooltip>
          <TooltipTrigger asChild><span>${(c / 100).toFixed(4)}</span></TooltipTrigger>
          <TooltipContent>Estimated, based on bundled pricing data.</TooltipContent>
        </Tooltip>
      )
    },
  },
]
```

- [ ] **Step 2: Test that the columns render with the right data**

```tsx
it('renders tokens and cost columns', async () => {
  // stub /api/runs to return a run with tokens_in: 100, tokens_out: 50, cost_cents: 5
  // render Runs
  // expect getByText(/100 \/ 50/), getByText('$0.0500')
})
```

- [ ] **Step 3: Commit**

```bash
git commit -m "refactor(ui): tokens and cost as dedicated DataTable columns"
```

---

## Task 5: Add "tokens this week / est. cost this week" tile to Dashboard

**Files:**
- Modify: `web/src/pages/Dashboard.tsx` — add a stats tile.
- Modify: `internal/webapi/runs.go` (or new file) — add `/api/runs/aggregate?since=…` returning sums.
- Test: webapi + vitest.

- [ ] **Step 1: Add a Go aggregate endpoint**

Failing test:

```go
// internal/webapi/runs_aggregate_test.go
func TestRunsAggregate_SumsLast7Days(t *testing.T) {
	// seed three runs: 100/50/2¢, 200/100/5¢, 300/150/8¢
	// hit /api/runs/aggregate?since=2026-04-23
	// expect {tokens_in: 600, tokens_out: 300, cost_cents: 15, count: 3}
}
```

- [ ] **Step 2: Add an sqlc query**

```sql
-- internal/db/queries/run.sql (append)

-- name: AggregateRunsSince :one
SELECT
  COUNT(*)::bigint                AS count,
  COALESCE(SUM(tokens_in), 0)::bigint  AS tokens_in,
  COALESCE(SUM(tokens_out), 0)::bigint AS tokens_out,
  COALESCE(SUM(cost_cents), 0)::bigint AS cost_cents
FROM run
WHERE org_id = $1 AND created_at >= $2;
```

Run: `make sqlc`.

- [ ] **Step 3: Implement the handler**

```go
// internal/webapi/runs_aggregate.go (new)
func (s *Server) handleRunsAggregate(w http.ResponseWriter, r *http.Request) {
	since, err := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
	if err != nil {
		// default to 7 days
		since = time.Now().Add(-7 * 24 * time.Hour)
	}
	orgID := s.OrgIDFromContext(r.Context())
	row, err := s.Q.AggregateRunsSince(r.Context(), gen.AggregateRunsSinceParams{
		OrgID: orgID, CreatedAt: since,
	})
	if err != nil { http.Error(w, err.Error(), 500); return }
	json.NewEncoder(w).Encode(row)
}
```

Register in `server.go`.

- [ ] **Step 4: Tests pass**

Run: `go test ./internal/webapi/ -run TestRunsAggregate -count=1`
Expected: PASS.

- [ ] **Step 5: Add the Dashboard tile**

```tsx
// in Dashboard.tsx
import { useQuery } from '@tanstack/react-query'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'

const { data: weekly } = useQuery({
  queryKey: ['runs-aggregate-7d'],
  queryFn: () => fetch('/api/runs/aggregate').then(r => r.json()),
})

<Card>
  <CardHeader><CardTitle className="text-sm font-medium text-muted-foreground">This week</CardTitle></CardHeader>
  <CardContent>
    <div className="text-2xl font-bold">
      {weekly ? `${(weekly.tokens_in + weekly.tokens_out).toLocaleString()} tokens` : '—'}
    </div>
    <div className="text-sm text-muted-foreground">
      est. {weekly ? `$${(weekly.cost_cents / 100).toFixed(2)}` : '—'} across {weekly?.count ?? 0} runs
    </div>
  </CardContent>
</Card>
```

- [ ] **Step 6: vitest**

```tsx
it('shows weekly tokens tile', async () => {
  // stub /api/runs/aggregate -> { tokens_in: 1000, tokens_out: 500, cost_cents: 250, count: 5 }
  // render Dashboard; expect /1,500 tokens/, /\$2\.50/, /5 runs/
})
```

- [ ] **Step 7: Commit**

```bash
git commit -m "feat(ui): weekly tokens/cost tile on Dashboard"
git commit -m "feat(api): /api/runs/aggregate endpoint"  # if separate
```

---

## Task 6: Add `auto_pause_after` to the manifest reference and schedule-authoring guide

This piggybacks on Phase 4 docs, but listed here so it isn't dropped.

- [ ] **Step 1: Confirm the manifest field is documented in `docs/reference/manifest.md`** (Phase 4 Task 4 should have generated it). If absent, add.

- [ ] **Step 2: Add a section to `docs/guides/schedule-authoring.md`** explaining the auto-pause semantics:
- Counter resets on a `succeeded` run
- Counter increments on `failed` (not `partial_failure` — verify against `internal/api/finalize.go`)
- Default is disabled (omit or set 0)
- Resume via the UI button or `cronfoundry admin … resume <schedule-id>` (if such command exists; if not, file a follow-up)

- [ ] **Step 3: Commit**

```bash
git commit -m "docs: auto-pause semantics in schedule-authoring guide"
```

---

## Task 7: Verification on deployed instance

- [ ] **Step 1: On the Phase-2 dogfood deployment**, exercise each feature manually:
  - Trigger 3 failed runs, observe auto-pause + Resume button.
  - Click "Run now" on an enabled schedule, observe the run dispatch and the RunDetailSheet open with live-tail.
  - Click "Run now" on the same schedule again while it's running — confirm 409 toast.
  - Watch the tokens/cost columns populate after a successful Copilot Enterprise run.
  - Verify the Dashboard weekly tile shows non-zero numbers.

- [ ] **Step 2: Capture screenshots** for Phase 4 docs.

- [ ] **Step 3: Note any issues** in `docs/superpowers/specs/<date>-phase5-verification.md`. Fix in focused commits.

---

## Self-review

- [ ] **Spec coverage:** the spec §Phase 5 calls out 5a/5b/5c. Each has at least one task above (verify, polish UI, screenshot).
- [ ] **No placeholders.** All steps have concrete code.
- [ ] **All tests pass:** `go test ./... && cd web && npm test`.
- [ ] **Build clean:** `make build`.

---

## Handoff

Phase 5 deliverables feed Phase 4 (docs reference for `auto_pause_after`, screenshots of all three features). Once Phase 5 verification passes, the polish-pass spec is done.
