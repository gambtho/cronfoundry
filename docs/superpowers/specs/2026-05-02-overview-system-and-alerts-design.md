# Overview side-rail cards (System & Alerts) — design

## Problem

Overview's right rail has two `<ComingSoon>` placeholder cards:

1. **System** — scheduler, queue depth, workers, last sync.
2. **Alerts & rotations** — operator-actionable signals (quiet jobs,
   expiring secrets, drift, recently auto-paused).

The UI is built. Both cards need backend endpoints.

## Approach

Two endpoints, both `GET`, both org-scoped, both stateless (no new
tables, no background jobs). The cards refetch on the same 30s
interval as the rest of Overview.

The Alerts card is intentionally narrow — only signals derivable
from the *current* schema land in v1. Speculative signals
(secrets-expiry, drift) are wired into the response shape with
empty arrays so the frontend doesn't reshape when those land.

## Endpoint 1: `GET /system/health`

Response:

```ts
type SystemHealth = {
  scheduler:    { status: 'healthy' | 'degraded' | 'down', last_tick_at: string | null }
  queue_depth:  number      // pending + running runs
  workers:      number      // distinct runner_pid among running runs; 0 if no running runs
  last_sync_at: string | null  // most recent run.created_at, or null if no runs
}
```

### Computation

- `scheduler.last_tick_at`: read from an in-process atomic that the
  scheduler updates each tick. Add `func (s *Scheduler) LastTickAt()
  time.Time` and inject the scheduler into webapi `Deps`.
- `scheduler.status`: `healthy` if `now - last_tick_at < 2×SweepInterval`,
  `degraded` if `< 5×SweepInterval`, else `down`. `down` also covers
  the "scheduler never ticked" case (`last_tick_at` zero).
- `queue_depth`: `SELECT count(*) FROM run WHERE status IN ('pending','running') AND org_id = $1`.
- `workers`: `SELECT count(DISTINCT runner_pid) FROM run WHERE status = 'running' AND org_id = $1 AND runner_pid IS NOT NULL`.
- `last_sync_at`: `SELECT MAX(created_at) FROM run WHERE org_id = $1`.

All queries are org-scoped. The scheduler tick is process-global,
not per-org — that's accurate (one scheduler serves all orgs in
this deployment model).

### Why no new state

Everything except `last_tick_at` is already in `run`. The tick
timestamp is one atomic store per sweep — cheaper than persisting,
and a process restart legitimately *should* show "scheduler not
yet ticked" until the first sweep.

## Endpoint 2: `GET /alerts`

Response:

```ts
type Alerts = {
  quiet_jobs:        QuietJob[]
  recently_paused:   PausedJob[]
  expiring_secrets:  []      // always empty in v1; reserved
  drift:             []      // always empty in v1; reserved
}

type QuietJob = {
  schedule_id:    string
  schedule_name:  string
  last_success:   string | null  // ISO; null if never succeeded
  expected_every: number          // seconds, derived from cron
}

type PausedJob = {
  schedule_id:   string
  schedule_name: string
  paused_at:     string
  reason:        string | null
}
```

### Quiet-job derivation

A schedule is "quiet" when its last successful run is older than
**3× its expected interval** (with a floor of 1 hour to avoid
noisy minutely-cron alerts on transient blips):

```sql
SELECT s.id, s.name,
       (SELECT MAX(finished_at) FROM run r
         WHERE r.schedule_id = s.id AND r.status = 'succeeded') AS last_success
  FROM schedule s
 WHERE s.org_id = $1
   AND s.paused_at IS NULL
   AND s.auto_paused_at IS NULL
HAVING last_success IS NULL
    OR now() - last_success > GREATEST(interval '1 hour', 3 * <expected_interval>);
```

`expected_interval` is computed in Go from the cron expression
(the scheduler already parses cron; reuse `cron.NextN` to derive
typical step). The query becomes a Go-side filter over a list of
candidate schedules — keeps SQL portable.

Cap result at 20 to bound payload; the card shows top N anyway.

### Recently-paused derivation

```sql
SELECT id, name, auto_paused_at, auto_pause_reason
  FROM schedule
 WHERE org_id = $1
   AND auto_paused_at IS NOT NULL
   AND auto_paused_at > now() - interval '7 days'
 ORDER BY auto_paused_at DESC
 LIMIT 20;
```

This is purely auto-pause (manual pauses are an explicit operator
action and don't warrant an alert).

### Reserved fields

`expiring_secrets` and `drift` are typed as empty arrays in v1.
The frontend renders them only when non-empty, so adding data
later is a backend-only change. We don't ship a stub — empty is
empty.

The card's existing "needs api support" copy is replaced with a
real empty state ("Nothing to rotate") when both arrays are empty
*and* the live arrays (`quiet_jobs`, `recently_paused`) are
empty too.

## Frontend

`api.system.health()` and `api.alerts.list()` join the existing
React Query refetch cadence on Overview (30s).

`ComingSoon` wrappers come off both cards. The body of each card
is mostly already written — it just reads from the new query
results instead of hardcoded placeholders.

The "Alerts & rotations" card renders a unified list:

- Each `quiet_jobs` entry → amber pill, "<name> hasn't succeeded
  in <relative time>".
- Each `recently_paused` → red pill, "<name> auto-paused: <reason>".
- Empty across all four arrays → muted "All quiet" state.

System card maps directly onto the existing KV rows; no layout
change.

## Authn / authz

Both endpoints use the existing org-bearer auth, same as
`/runs` and `/schedules`. No new permission model.

## Performance

`/system/health` is three small aggregates, all hitting indexed
columns; comfortably under 10ms on any sane org.
`/alerts` is two queries plus Go-side cron-interval calculation
over (at most) the schedule list; same scale.

Neither response is cached server-side — the 30s client refetch
is the cache.

## Testing

- Repository-level tests for each derivation (quiet, paused,
  workers, queue depth).
- Handler tests for org isolation on both endpoints.
- Frontend: empty-state rendering, transition from `<ComingSoon>`
  removal (snapshot that the cards render with mocked responses).

## Out of scope (deferred)

- **Expiring secrets**: `secret` table doesn't carry expiry; needs
  a schema change + per-provider expiry probing. Track separately.
- **Drift detection**: requires a "what should be running" baseline
  comparison the system doesn't maintain. Track separately.
- **Multi-tenant scheduler health**: the deployment is single
  scheduler today; if that ever shards, `scheduler` becomes an
  array. Not now.
- **Historical health timeline**: the card shows current state.
  A "queue depth over time" chart is a separate feature.
