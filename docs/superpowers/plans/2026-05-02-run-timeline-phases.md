# Run Timeline Phases Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Emit `phase.enter` boundary events from the runner so the Run-detail page can render a segmented boot/secrets/exec/publish/fail timeline, with no schema or endpoint changes.

**Architecture:** A new `EventPhaseEnter` event type is added alongside the existing MCP event types in `internal/runner/runner.go`. A small `enterPhase(name string)` helper writes through the existing `EventSink`. Boundary calls are inserted at four happy-path transitions and one failure cap. The frontend reduces these events into segments client-side.

**Tech Stack:** Go (runner + webapi unchanged), TypeScript/React (frontend reducer), existing `run_event` table, existing `GET /api/runs/{id}/events` endpoint and SSE stream.

**Spec:** `docs/superpowers/specs/2026-05-02-run-timeline-phases-design.md`

---

## File Structure

- Modify: `internal/runner/runner.go` — add event type + helper + boundary calls
- Modify: `internal/runner/runner_test.go` — assert ordered emission
- Create: `web/src/lib/timeline.ts` — pure reducer events → segments
- Create: `web/src/lib/timeline.test.ts` — reducer tests
- Modify: `web/src/pages/RunDetail.tsx` — render the segmented bar
- Modify: `web/src/lib/types.ts` — extend RunEvent payload typing (no shape change)

---

### Task 1: Add `EventPhaseEnter` constant + `enterPhase` helper

**Files:**
- Modify: `internal/runner/runner.go` (around the existing `EventType` const block, lines 86–95)

- [ ] **Step 1: Add the constant**

In `internal/runner/runner.go`, in the `const ( ... EventType ... )` block, add:

```go
// EventPhaseEnter marks entry into a coarse lifecycle phase
// (boot|secrets|exec|publish|fail). Payload: {"phase": "<name>"} on
// the happy path; {"phase":"fail","prev":"<name>"} on terminal error.
EventPhaseEnter EventType = "phase.enter"
```

- [ ] **Step 2: Add a private helper on `*Runner`**

Append below `New(...)`:

```go
// enterPhase emits a phase.enter boundary event. The runner tracks
// `last` so the failure path can record which phase was active at
// the time of the error. Pass prev="" on the happy path.
func (r *Runner) enterPhase(phase, prev string) {
    payload := map[string]any{"phase": phase}
    if prev != "" {
        payload["prev"] = prev
    }
    r.deps.EventSink(RunEvent{Type: EventPhaseEnter, Payload: payload})
}
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: clean build (helper unused for now is fine — not exported).

- [ ] **Step 4: Commit**

```bash
git add internal/runner/runner.go
git commit -m "feat(runner): add EventPhaseEnter type + enterPhase helper"
```

---

### Task 2: Wire boundary calls into the happy path

**Files:**
- Modify: `internal/runner/runner.go` `Run()` function

- [ ] **Step 1: Track the active phase locally**

Inside `Run()`, immediately after `result := RunResult{StartedAt: r.deps.Now()}` (line 137), insert:

```go
// active tracks the most-recently-entered phase so the failure
// branch can record `prev` accurately. Updated only via the
// enterPhase wrapper below.
active := ""
enter := func(phase string) {
    r.enterPhase(phase, "")
    active = phase
}
fail := func(err error) (RunResult, error) {
    r.enterPhase("fail", active)
    return failHelper(&result, err, r.deps.Now)
}
failKind := func(kind string, err error) (RunResult, error) {
    r.enterPhase("fail", active)
    return failWithKind(&result, kind, err, r.deps.Now)
}

enter("boot")
```

(`failHelper` is the existing `fail(...)` package-level function; the closure shadow exists only to add the boundary event before delegating.)

- [ ] **Step 2: Replace existing `fail(...)` and `failWithKind(...)` calls in `Run`**

In `Run()` only (not other functions), replace every `return fail(&result, err, r.deps.Now)` with `return fail(err)`. Replace every `return failWithKind(&result, kind, err, r.deps.Now)` with `return failKind(kind, err)`. The package-level `fail` is renamed to `failHelper` to avoid the shadow conflict — update its definition and all *non-Run* call sites accordingly. Verify with:

```
grep -n "fail(\|failWithKind(" internal/runner/runner.go
```

Expected: only the two closures + the package-level `failHelper`/`failWithKind` definitions remain.

- [ ] **Step 3: Insert `enter("secrets")` before secret resolution**

The runner does not have a discrete secret-resolution stage — secrets are resolved per-MCP-server inside the tool-aware loop, and per-publisher inside `Dispatcher.Dispatch`. The `secrets` phase covers the *prep* between manifest parse and provider construction. Insert immediately after `envBanner := buildEnvBanner(sch.Env)` (around line 171):

```go
enter("secrets")
```

This is the boundary the UI cares about; per-publisher secret resolution stays inside `publish`.

- [ ] **Step 4: Insert `enter("exec")` before LLM work**

Immediately after `var llmOutput string` (around line 178):

```go
enter("exec")
```

- [ ] **Step 5: Insert `enter("publish")` before destinations dispatch**

Immediately before `dispatcher := &publish.Dispatcher{...}` (around line 342):

```go
enter("publish")
```

If the run is `DryRun` it returns before this point — that's correct, dry runs have no publish phase.

- [ ] **Step 6: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/runner/runner.go
git commit -m "feat(runner): emit phase.enter at boot/secrets/exec/publish + fail boundaries"
```

---

### Task 3: Test runner phase emissions

**Files:**
- Modify: `internal/runner/runner_test.go`

- [ ] **Step 1: Write the happy-path test**

Append to `runner_test.go`:

```go
func TestRun_EmitsPhaseEnterInOrder_HappyPath(t *testing.T) {
    var got []string
    sink := func(e RunEvent) {
        if e.Type == EventPhaseEnter {
            got = append(got, e.Payload["phase"].(string))
        }
    }
    // Reuse the existing happy-path harness from this file (see
    // TestRun_NonToolPath). Inject sink via Deps.EventSink.
    runHappyPath(t, withSink(sink)) // helper from existing tests; if absent, build minimally as in TestRun_NonToolPath

    require.Equal(t, []string{"boot", "secrets", "exec", "publish"}, got)
}
```

If `runHappyPath`/`withSink` don't exist, copy the construction from the nearest existing happy-path test in the same file and inline `Deps{EventSink: sink, ...}`.

- [ ] **Step 2: Write the failure-path test**

```go
func TestRun_EmitsFailWithPrev_OnExecError(t *testing.T) {
    var got []map[string]any
    sink := func(e RunEvent) {
        if e.Type == EventPhaseEnter {
            got = append(got, e.Payload)
        }
    }
    // Force an error inside the exec phase by wiring a provider that
    // returns an error from Chat. See existing failure tests for the
    // pattern.
    runWithFailingProvider(t, withSink(sink))

    last := got[len(got)-1]
    require.Equal(t, "fail", last["phase"])
    require.Equal(t, "exec", last["prev"])
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/runner/ -run TestRun_EmitsPhase -v`
Expected: both PASS.

- [ ] **Step 4: Run the full runner test suite**

Run: `go test ./internal/runner/...`
Expected: all PASS (no regressions in existing tests — they ignore unknown event types).

- [ ] **Step 5: Commit**

```bash
git add internal/runner/runner_test.go
git commit -m "test(runner): assert phase.enter emission order + fail-with-prev"
```

---

### Task 4: Frontend reducer + tests

**Files:**
- Create: `web/src/lib/timeline.ts`
- Create: `web/src/lib/timeline.test.ts`

- [ ] **Step 1: Write the failing reducer test**

`web/src/lib/timeline.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { reduceTimeline, type TimelineSegment } from './timeline'

const ev = (ts: string, phase: string, prev?: string) => ({
  id: 1, run_id: 'r', ts, level: 'info',
  event_type: 'phase.enter',
  payload_json: prev ? { phase, prev } : { phase },
})

describe('reduceTimeline', () => {
  it('produces ordered segments from happy-path boundaries', () => {
    const events = [
      ev('2026-05-02T10:00:00Z', 'boot'),
      ev('2026-05-02T10:00:01Z', 'secrets'),
      ev('2026-05-02T10:00:02Z', 'exec'),
      ev('2026-05-02T10:00:30Z', 'publish'),
    ]
    const segs = reduceTimeline(events as any, '2026-05-02T10:00:31Z')
    expect(segs.map((s: TimelineSegment) => s.phase))
      .toEqual(['boot', 'secrets', 'exec', 'publish'])
    expect(segs[3].end).toBe('2026-05-02T10:00:31Z')
    expect(segs.every(s => !s.failed)).toBe(true)
  })

  it('caps the bar with fail and marks the previous segment failed', () => {
    const events = [
      ev('2026-05-02T10:00:00Z', 'boot'),
      ev('2026-05-02T10:00:01Z', 'secrets'),
      ev('2026-05-02T10:00:02Z', 'exec'),
      ev('2026-05-02T10:00:05Z', 'fail', 'exec'),
    ]
    const segs = reduceTimeline(events as any, '2026-05-02T10:00:05Z')
    // Three rendered segments: boot, secrets, exec(failed). fail itself is zero-width.
    expect(segs.map(s => s.phase)).toEqual(['boot', 'secrets', 'exec'])
    expect(segs[2].failed).toBe(true)
    expect(segs[2].end).toBe('2026-05-02T10:00:05Z')
  })

  it('runs the last segment to "now" when run is not finished', () => {
    const events = [ev('2026-05-02T10:00:00Z', 'boot')]
    const now = '2026-05-02T10:00:10Z'
    const segs = reduceTimeline(events as any, null, now)
    expect(segs).toHaveLength(1)
    expect(segs[0].end).toBe(now)
  })

  it('returns empty array when there are no phase events', () => {
    expect(reduceTimeline([] as any, null)).toEqual([])
  })
})
```

- [ ] **Step 2: Run test, verify failure**

Run: `cd web && npx vitest run src/lib/timeline.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement the reducer**

`web/src/lib/timeline.ts`:

```ts
import type { RunEvent } from './types'

export type PhaseName = 'boot' | 'secrets' | 'exec' | 'publish' | 'fail'

export type TimelineSegment = {
  phase:  PhaseName
  start:  string  // ISO
  end:    string  // ISO
  failed: boolean
}

type Boundary = { ts: string; phase: PhaseName; prev?: PhaseName }

/**
 * Reduce a list of run events into rendered timeline segments.
 *
 *  - Filters to event_type === 'phase.enter'.
 *  - Pairs consecutive boundaries; the last open segment runs to
 *    `runFinishedAt` if the run is terminal, else to `nowISO`.
 *  - The terminal `fail` boundary is *not* rendered as its own
 *    segment. Instead, the segment immediately preceding it is
 *    marked failed and capped at fail's timestamp.
 */
export function reduceTimeline(
  events: RunEvent[],
  runFinishedAt: string | null,
  nowISO: string = new Date().toISOString(),
): TimelineSegment[] {
  const boundaries: Boundary[] = events
    .filter(e => e.event_type === 'phase.enter' && e.payload_json && typeof e.payload_json === 'object')
    .map(e => {
      const p = e.payload_json as { phase: PhaseName; prev?: PhaseName }
      return { ts: e.ts, phase: p.phase, prev: p.prev }
    })

  if (boundaries.length === 0) return []

  const segments: TimelineSegment[] = []
  for (let i = 0; i < boundaries.length; i++) {
    const b = boundaries[i]
    if (b.phase === 'fail') continue  // handled by tagging the previous segment

    const next = boundaries[i + 1]
    const isLast = i === boundaries.length - 1
    const end = next
      ? next.ts
      : (runFinishedAt ?? nowISO)
    const failed = next?.phase === 'fail' && next.prev === b.phase
    segments.push({ phase: b.phase, start: b.ts, end, failed })
    void isLast
  }
  return segments
}
```

- [ ] **Step 4: Run tests, verify pass**

Run: `cd web && npx vitest run src/lib/timeline.test.ts`
Expected: all 4 PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/timeline.ts web/src/lib/timeline.test.ts
git commit -m "feat(web): timeline reducer for phase.enter events"
```

---

### Task 5: Render the segmented bar in RunDetail

**Files:**
- Modify: `web/src/pages/RunDetail.tsx`

- [ ] **Step 1: Locate the existing timeline placeholder**

```
grep -n "timeline\|phase\|segment" web/src/pages/RunDetail.tsx
```

If no placeholder exists, add the bar above the run's events list. If a static placeholder exists, replace it.

- [ ] **Step 2: Add the component**

Insert at the top of `RunDetail.tsx` (or in an adjacent file `web/src/components/run/PhaseBar.tsx` — pick whichever matches existing conventions for that page; if RunDetail is already large, prefer the separate file):

```tsx
import { reduceTimeline, type TimelineSegment } from '../lib/timeline'
import type { Run, RunEvent } from '../lib/types'

export function PhaseBar({ run, events }: { run: Run; events: RunEvent[] }) {
  const finished = run.finished_at ?? null
  const segments = reduceTimeline(events, finished)
  if (segments.length === 0) return null

  const total = segments.reduce(
    (n, s) => n + Math.max(1, Date.parse(s.end) - Date.parse(s.start)),
    0,
  )

  return (
    <div className="flex h-2 w-full overflow-hidden rounded bg-rule">
      {segments.map((s, i) => {
        const dur  = Math.max(1, Date.parse(s.end) - Date.parse(s.start))
        const pct  = (dur / total) * 100
        const tone = s.failed ? 'bg-red-500' : phaseColor(s.phase)
        return (
          <div
            key={i}
            title={`${s.phase}${s.failed ? ' (failed)' : ''} · ${Math.round(dur / 100) / 10}s`}
            className={tone}
            style={{ width: `${pct}%` }}
          />
        )
      })}
    </div>
  )
}

function phaseColor(p: TimelineSegment['phase']): string {
  switch (p) {
    case 'boot':    return 'bg-zinc-400'
    case 'secrets': return 'bg-zinc-500'
    case 'exec':    return 'bg-emerald-500'
    case 'publish': return 'bg-sky-500'
    default:        return 'bg-zinc-300'
  }
}
```

(Color tokens may not exist as written — match whatever the design system uses on Run-detail today; the brainstorming spec didn't pin colors.)

- [ ] **Step 3: Mount the component in RunDetail**

In the run-detail JSX, above the events list:

```tsx
<PhaseBar run={run} events={events} />
```

`run` and `events` are already in scope from the existing queries on the page.

- [ ] **Step 4: Smoke-build**

Run: `cd web && npm run build`
Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/RunDetail.tsx web/src/components/run/PhaseBar.tsx
git commit -m "feat(web): segmented phase bar on run detail"
```

---

### Task 6: End-to-end sanity

- [ ] **Step 1: Run the full backend test suite**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 2: Run the full frontend test suite**

Run: `cd web && npx vitest run`
Expected: all PASS.

- [ ] **Step 3: Manual smoke (optional)**

Start a dev environment, fire a run, open Run detail. Verify the bar renders four segments on a successful run and three (with the last red) on a forced failure. Document the result in the PR description.
