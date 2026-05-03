# Overview System + Alerts Cards Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the two `<ComingSoon>` cards on the Overview page with live data from new `GET /api/system/health` and `GET /api/alerts` endpoints.

**Architecture:** Both endpoints are stateless and read-only — `system/health` aggregates run counts plus an in-memory scheduler-tick atomic; `alerts` derives quiet-job and recently-paused signals from the existing `schedule`/`run` tables. No new tables.

**Tech Stack:** Go (sqlc, pgx), Postgres, TypeScript/React.

**Spec:** `docs/superpowers/specs/2026-05-02-overview-system-and-alerts-design.md`

---

## File Structure

- Modify: `internal/scheduler/loop.go` — record `LastTickAt`
- Create: `internal/scheduler/tick_clock.go` — atomic accessor pair
- Create: `internal/scheduler/tick_clock_test.go`
- Modify: `internal/webapi/server.go` — add deps wiring + routes
- Create: `internal/webapi/system.go`
- Create: `internal/webapi/system_test.go`
- Create: `internal/webapi/alerts.go`
- Create: `internal/webapi/alerts_test.go`
- Create: `internal/db/queries/health.sql` — three small aggregates
- Create: `internal/db/queries/alerts.sql` — two queries
- Regenerate: `internal/db/gen/`
- Modify: `web/src/lib/api.ts`, `web/src/lib/types.ts`
- Modify: `web/src/pages/Overview.tsx` — drop `<ComingSoon>` wrappers, wire queries

---

### Task 1: Scheduler tick clock

**Files:**
- Create: `internal/scheduler/tick_clock.go`
- Create: `internal/scheduler/tick_clock_test.go`
- Modify: `internal/scheduler/loop.go`

- [ ] **Step 1: Write the failing test**

`internal/scheduler/tick_clock_test.go`:

```go
package scheduler

import (
    "testing"
    "time"
)

func TestTickClock_RecordAndRead(t *testing.T) {
    var c TickClock
    if !c.LastTickAt().IsZero() {
        t.Fatal("zero value should be zero time")
    }
    now := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
    c.Record(now)
    if got := c.LastTickAt(); !got.Equal(now) {
        t.Fatalf("got %v, want %v", got, now)
    }
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/scheduler/ -run TestTickClock`
Expected: FAIL — undefined `TickClock`.

- [ ] **Step 3: Implement**

`internal/scheduler/tick_clock.go`:

```go
package scheduler

import (
    "sync/atomic"
    "time"
)

// TickClock records the wall-clock time of the most recent successful
// scheduler tick. Read-side is lock-free; safe for concurrent readers.
type TickClock struct {
    unixNano atomic.Int64
}

func (c *TickClock) Record(t time.Time) { c.unixNano.Store(t.UnixNano()) }

func (c *TickClock) LastTickAt() time.Time {
    n := c.unixNano.Load()
    if n == 0 {
        return time.Time{}
    }
    return time.Unix(0, n)
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/scheduler/ -run TestTickClock`
Expected: PASS.

- [ ] **Step 5: Wire into Loop**

In `internal/scheduler/loop.go`, extend `Deps` (or wherever `tickOnce` lives) with a `*TickClock` field. After a successful `Tick`, call `deps.Clock.Record(time.Now())`. Where `Loop` is called from (look in `cmd/cronfoundry/`), construct and share a single `TickClock` so the webapi handler can read it.

- [ ] **Step 6: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/scheduler/
git commit -m "feat(scheduler): TickClock — atomic last-tick timestamp"
```

---

### Task 2: SQL aggregates

**Files:**
- Create: `internal/db/queries/health.sql`
- Create: `internal/db/queries/alerts.sql`
- Regenerate: `internal/db/gen/`

- [ ] **Step 1: health.sql**

```sql
-- name: CountQueueDepth :one
SELECT count(*)::bigint
FROM run
WHERE org_id = $1 AND status IN ('pending','running');

-- name: CountActiveWorkers :one
SELECT count(DISTINCT runner_pid)::bigint
FROM run
WHERE org_id = $1 AND status = 'running' AND runner_pid IS NOT NULL;

-- name: LastRunCreatedAt :one
SELECT MAX(created_at)::timestamptz
FROM run
WHERE org_id = $1;
```

- [ ] **Step 2: alerts.sql**

```sql
-- name: ListSchedulesForQuietCheck :many
SELECT s.id, s.name, s.cron,
       (SELECT MAX(r.finished_at) FROM run r
         WHERE r.schedule_id = s.id AND r.status = 'succeeded') AS last_success
  FROM schedule s
 WHERE s.org_id = $1
   AND s.paused_at IS NULL
   AND s.auto_paused_at IS NULL
 ORDER BY s.name ASC;

-- name: ListRecentAutoPaused :many
SELECT id, name, auto_paused_at, auto_pause_reason
  FROM schedule
 WHERE org_id = $1
   AND auto_paused_at IS NOT NULL
   AND auto_paused_at > now() - interval '7 days'
 ORDER BY auto_paused_at DESC
 LIMIT 20;
```

(Adjust column names if `schedule.cron` is actually `cron_expr` etc. — verify with `grep -n "cron" internal/db/migrations/*schedule*.sql`.)

- [ ] **Step 3: Regenerate sqlc**

Run: `make sqlc`
Expected: generated code refreshed.

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/db/
git commit -m "feat(db): queries for /system/health and /alerts"
```

---

### Task 3: `/api/system/health` handler

**Files:**
- Create: `internal/webapi/system.go`
- Create: `internal/webapi/system_test.go`
- Modify: `internal/webapi/server.go`

- [ ] **Step 1: Add `Clock` + `SweepInterval` to webapi Deps**

In `internal/webapi/server.go`, on `Deps`:

```go
// Clock provides the most recent scheduler tick time. May be nil in
// test environments — handlers degrade gracefully (status="down").
Clock *scheduler.TickClock
// SweepInterval is the configured cadence between scheduler ticks.
// Used to classify scheduler health: <2x healthy, <5x degraded, else down.
SweepInterval time.Duration
```

Wire from `cmd/cronfoundry/` so the webapi gets the same TickClock the scheduler writes to.

- [ ] **Step 2: Handler**

`internal/webapi/system.go`:

```go
package webapi

import (
    "net/http"
    "time"

    "github.com/jackc/pgx/v5/pgtype"
)

type systemHealthDTO struct {
    Scheduler   schedulerDTO `json:"scheduler"`
    QueueDepth  int64        `json:"queue_depth"`
    Workers     int64        `json:"workers"`
    LastSyncAt  *string      `json:"last_sync_at"`
}

type schedulerDTO struct {
    Status     string  `json:"status"`        // healthy | degraded | down
    LastTickAt *string `json:"last_tick_at"`  // ISO or null
}

type systemHandler struct{ deps Deps }

func (h *systemHandler) health(w http.ResponseWriter, r *http.Request) {
    orgID := orgIDFromContext(r.Context())  // use existing helper

    qd, err := h.deps.Queries.CountQueueDepth(r.Context(), orgID)
    if err != nil { writeErr(w, http.StatusInternalServerError, "queue", "internal"); return }
    workers, err := h.deps.Queries.CountActiveWorkers(r.Context(), orgID)
    if err != nil { writeErr(w, http.StatusInternalServerError, "workers", "internal"); return }
    last, err := h.deps.Queries.LastRunCreatedAt(r.Context(), orgID)
    if err != nil { writeErr(w, http.StatusInternalServerError, "last sync", "internal"); return }

    var lastTick *string
    sStatus := "down"
    if h.deps.Clock != nil {
        t := h.deps.Clock.LastTickAt()
        if !t.IsZero() {
            iso := t.UTC().Format(time.RFC3339)
            lastTick = &iso
            age := time.Since(t)
            switch {
            case age < 2*h.deps.SweepInterval:
                sStatus = "healthy"
            case age < 5*h.deps.SweepInterval:
                sStatus = "degraded"
            }
        }
    }
    var lastSync *string
    if last.Valid {  // pgtype.Timestamptz — adjust to actual generated type
        iso := last.Time.UTC().Format(time.RFC3339)
        lastSync = &iso
    }
    _ = pgtype.UUID{}  // import keeper if unused otherwise; remove if not needed

    writeJSON(w, http.StatusOK, systemHealthDTO{
        Scheduler:  schedulerDTO{Status: sStatus, LastTickAt: lastTick},
        QueueDepth: qd, Workers: workers, LastSyncAt: lastSync,
    })
}
```

- [ ] **Step 3: Register**

In `server.go`:

```go
sh2 := &systemHandler{deps: deps}
mux.Handle("GET /api/system/health", session(http.HandlerFunc(sh2.health)))
```

- [ ] **Step 4: Tests**

`internal/webapi/system_test.go`:
- A run row for org A in `running` state → `queue_depth >= 1`, `workers >= 1` for org A; org B sees 0.
- TickClock zero → `scheduler.status == "down"`, `last_tick_at == null`.
- TickClock recent → `"healthy"`.
- TickClock 3× SweepInterval old → `"degraded"`.
- TickClock 6× SweepInterval old → `"down"`.

Use the existing webapi test fixture pattern (see `events_test.go` or `audit_test.go`).

- [ ] **Step 5: Run**

Run: `go test ./internal/webapi/ -run System -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/webapi/system.go internal/webapi/system_test.go internal/webapi/server.go
git commit -m "feat(webapi): GET /api/system/health"
```

---

### Task 4: `/api/alerts` handler

**Files:**
- Create: `internal/webapi/alerts.go`
- Create: `internal/webapi/alerts_test.go`
- Modify: `internal/webapi/server.go`

- [ ] **Step 1: Handler skeleton**

`internal/webapi/alerts.go`:

```go
package webapi

import (
    "net/http"
    "time"

    "github.com/gambtho/cronfoundry/internal/scheduler/cron"  // or wherever cron lives
)

type alertsDTO struct {
    QuietJobs        []quietJobDTO `json:"quiet_jobs"`
    RecentlyPaused   []pausedDTO   `json:"recently_paused"`
    ExpiringSecrets  []struct{}    `json:"expiring_secrets"`
    Drift            []struct{}    `json:"drift"`
}

type quietJobDTO struct {
    ScheduleID    string  `json:"schedule_id"`
    ScheduleName  string  `json:"schedule_name"`
    LastSuccess   *string `json:"last_success"`
    ExpectedEvery int64   `json:"expected_every"`  // seconds
}

type pausedDTO struct {
    ScheduleID   string  `json:"schedule_id"`
    ScheduleName string  `json:"schedule_name"`
    PausedAt     string  `json:"paused_at"`
    Reason       *string `json:"reason"`
}

type alertsHandler struct{ deps Deps }

func (h *alertsHandler) list(w http.ResponseWriter, r *http.Request) {
    orgID := orgIDFromContext(r.Context())

    cands, err := h.deps.Queries.ListSchedulesForQuietCheck(r.Context(), orgID)
    if err != nil { writeErr(w, http.StatusInternalServerError, "alerts", "internal"); return }

    quiet := make([]quietJobDTO, 0)
    now := time.Now()
    for _, c := range cands {
        interval, ok := expectedInterval(c.Cron)
        if !ok { continue }
        threshold := 3 * interval
        if threshold < time.Hour { threshold = time.Hour }

        var lastISO *string
        var lastT time.Time
        if c.LastSuccess.Valid {
            lastT = c.LastSuccess.Time
            iso := lastT.UTC().Format(time.RFC3339)
            lastISO = &iso
        }
        isQuiet := !c.LastSuccess.Valid || now.Sub(lastT) > threshold
        if !isQuiet { continue }
        quiet = append(quiet, quietJobDTO{
            ScheduleID: uuidString(c.ID), ScheduleName: c.Name,
            LastSuccess: lastISO, ExpectedEvery: int64(interval / time.Second),
        })
        if len(quiet) >= 20 { break }
    }

    paused, err := h.deps.Queries.ListRecentAutoPaused(r.Context(), orgID)
    if err != nil { writeErr(w, http.StatusInternalServerError, "alerts", "internal"); return }
    pausedOut := make([]pausedDTO, len(paused))
    for i, p := range paused {
        pausedOut[i] = pausedDTO{
            ScheduleID: uuidString(p.ID), ScheduleName: p.Name,
            PausedAt: p.AutoPausedAt.Time.UTC().Format(time.RFC3339),
            Reason:   p.AutoPauseReason,
        }
    }

    writeJSON(w, http.StatusOK, alertsDTO{
        QuietJobs: quiet, RecentlyPaused: pausedOut,
        ExpiringSecrets: []struct{}{}, Drift: []struct{}{},
    })
}

// expectedInterval estimates the typical step between consecutive
// fires of a cron expression by computing two consecutive nexts and
// taking the difference. Returns (0, false) on parse error.
func expectedInterval(expr string) (time.Duration, bool) {
    s, err := cron.Parse(expr)  // use the project's existing cron parser
    if err != nil { return 0, false }
    t1 := s.Next(time.Now())
    t2 := s.Next(t1)
    return t2.Sub(t1), true
}
```

(`cron.Parse`/`Next` may have different names — `internal/scheduler/cron.go` is the place to look.)

- [ ] **Step 2: Register**

```go
ah2 := &alertsHandler{deps: deps}
mux.Handle("GET /api/alerts", session(http.HandlerFunc(ah2.list)))
```

- [ ] **Step 3: Tests**

Cases:
- Schedule that succeeded 5 minutes ago on a 1-minute cron → not quiet.
- Schedule that last succeeded 4 hours ago on a 1-hour cron → quiet.
- Schedule that never succeeded → quiet.
- Schedule paused 3 days ago via auto_pause → in `recently_paused`.
- Schedule auto-paused 10 days ago → not in `recently_paused`.
- Org isolation: schedule in org B does not appear in org A's response.

- [ ] **Step 4: Run**

Run: `go test ./internal/webapi/ -run Alerts -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/webapi/alerts.go internal/webapi/alerts_test.go internal/webapi/server.go
git commit -m "feat(webapi): GET /api/alerts (quiet jobs + recently auto-paused)"
```

---

### Task 5: Frontend types + API

**Files:**
- Modify: `web/src/lib/types.ts`
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Types**

```ts
export type SystemHealth = {
  scheduler:    { status: 'healthy' | 'degraded' | 'down', last_tick_at: string | null }
  queue_depth:  number
  workers:      number
  last_sync_at: string | null
}

export type Alerts = {
  quiet_jobs: {
    schedule_id:    string
    schedule_name:  string
    last_success:   string | null
    expected_every: number
  }[]
  recently_paused: {
    schedule_id:   string
    schedule_name: string
    paused_at:     string
    reason:        string | null
  }[]
  expiring_secrets: never[]
  drift:            never[]
}
```

- [ ] **Step 2: API**

```ts
export const api = {
  // ...existing...
  system: {
    health: () => http<SystemHealth>('/api/system/health'),
  },
  alerts: {
    list: () => http<Alerts>('/api/alerts'),
  },
}
```

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/
git commit -m "feat(web): types + client for /api/system/health and /api/alerts"
```

---

### Task 6: Replace `<ComingSoon>` cards on Overview

**Files:**
- Modify: `web/src/pages/Overview.tsx`

- [ ] **Step 1: Add queries**

In the Overview function, alongside `schedulesQ` and `recentRunsQ`:

```tsx
const healthQ = useQuery({
  queryKey: ['system', 'health'],
  queryFn:  api.system.health,
  refetchInterval: 30_000,
})
const alertsQ = useQuery({
  queryKey: ['alerts'],
  queryFn:  api.alerts.list,
  refetchInterval: 30_000,
})
```

- [ ] **Step 2: Replace the System card**

Delete the `<ComingSoon label="Coming soon">` wrapper around the System card. Replace the placeholder rows with live data:

```tsx
<Card>
  <Card.Header>System</Card.Header>
  <Card.Body>
    <KV>
      <KV.Row label="Scheduler">
        <Pill variant={
          healthQ.data?.scheduler.status === 'healthy' ? 'ok'
          : healthQ.data?.scheduler.status === 'degraded' ? 'amber'
          : 'fail'
        }>
          {healthQ.data?.scheduler.status ?? '—'}
        </Pill>
      </KV.Row>
      <KV.Row label="Queue depth">
        {healthQ.data ? `${healthQ.data.queue_depth} pending` : '—'}
      </KV.Row>
      <KV.Row label="Workers">
        {healthQ.data ? String(healthQ.data.workers) : '—'}
      </KV.Row>
      <KV.Row label="Last sync">
        {healthQ.data?.last_sync_at ? relativeTime(healthQ.data.last_sync_at) : '—'}
      </KV.Row>
    </KV>
  </Card.Body>
</Card>
```

- [ ] **Step 3: Replace the Alerts card**

```tsx
<Card>
  <Card.Header>Alerts &amp; rotations</Card.Header>
  <Card.Body>
    <AlertsList data={alertsQ.data} />
  </Card.Body>
</Card>
```

Where `AlertsList` (defined inline at the bottom of the file or in a sibling file) renders one item per quiet job and per recently-paused, with an "All quiet" empty state when both lists are empty.

```tsx
function AlertsList({ data }: { data?: Alerts }) {
  if (!data) return <p className="text-ink-3">—</p>
  const items: { tone: 'amber' | 'fail', text: string, sub?: string }[] = [
    ...data.quiet_jobs.map(q => ({
      tone: 'amber' as const,
      text: `${q.schedule_name} hasn't succeeded`,
      sub:  q.last_success ? `last ok ${relativeTime(q.last_success)}` : 'never succeeded',
    })),
    ...data.recently_paused.map(p => ({
      tone: 'fail' as const,
      text: `${p.schedule_name} auto-paused`,
      sub:  p.reason ?? undefined,
    })),
  ]
  if (items.length === 0) return <p className="text-ink-3">All quiet.</p>
  return (
    <ul className="m-0 flex list-none flex-col gap-3 p-0 text-[12px]">
      {items.map((it, i) => (
        <li key={i} className="flex gap-3">
          <Pill variant={it.tone}>{it.tone === 'fail' ? 'paused' : 'quiet'}</Pill>
          <span>
            {it.text}
            {it.sub && (
              <span className="mt-0.5 block font-mono text-[10px] uppercase tracking-[0.06em] text-ink-3">
                {it.sub}
              </span>
            )}
          </span>
        </li>
      ))}
    </ul>
  )
}
```

Remove the `ComingSoon` import if no longer used.

- [ ] **Step 4: Build**

Run: `cd web && npm run build`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/Overview.tsx
git commit -m "feat(web): live System + Alerts cards on Overview"
```

---

### Task 7: End-to-end sanity

- [ ] **Step 1**: `go test ./...`
- [ ] **Step 2**: `cd web && npx vitest run && npm run build`
- [ ] **Step 3**: Manual smoke — Overview page should show real values; the `<ComingSoon>` overlays are gone.
