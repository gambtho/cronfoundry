# P7 — MVP Close-out — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the three remaining MVP items outside P6 — a live-tail log UI component, a status/roadmap refresh across `README.md` + `docs/index.html`, and a once-through Azure smoke runbook committed as a guide.

**Architecture:** One React component (`LogTail.tsx`) consuming the already-shipped SSE endpoint `GET /api/runs/{id}/events/stream` with a static-mode fallback that fetches `/api/runs/{id}/events` for finished runs. Two targeted doc edits. One new guide file. No Go code changes, no DB migrations, no new endpoints.

**Tech Stack:** React 18, TypeScript 5, Vitest 1, `@testing-library/react` (new devDep), jsdom (new devDep), Tailwind CSS, native `EventSource`. Design spec: `docs/superpowers/specs/2026-04-20-p7-mvp-closeout-design.md`. Depends on: P6 (`docs/superpowers/plans/2026-04-20-p6-mvp-gaps.md`) for the audit-verification step of the Azure runbook.

---

## File Map

### React — `web/`

| File | Action | Purpose |
|---|---|---|
| `web/package.json` | Modify | Add `@testing-library/react`, `@testing-library/jest-dom`, `jsdom`; add `"test"` script |
| `web/vitest.config.ts` | Create | Vitest config with jsdom environment + setup file |
| `web/src/test/setup.ts` | Create | `@testing-library/jest-dom` matcher registration |
| `web/src/lib/api.ts` | Modify | Add `api.runs.eventsStreamURL(id)` |
| `web/src/components/LogTail.tsx` | Create | Live-tail panel (streaming + static modes) |
| `web/src/components/LogTail.test.tsx` | Create | Vitest unit tests |
| `web/src/pages/Runs.tsx` | Modify | Replace inline event timeline in `RunDetail` with `<LogTail>` |

### Docs

| File | Action | Purpose |
|---|---|---|
| `README.md` | Modify | Status → "MVP shipped"; roadmap → deferred list pointer; architecture package list refresh |
| `docs/index.html` | Modify | Status/roadmap copy → "MVP shipped"; one-line self-host-on-Azure beat |
| `docs/guides/smoke-test-mvp-azure.md` | Create | End-to-end Azure runbook, executed once by the author |

---

## Phase 1 — Vitest scaffolding

### Task 1: Install testing libs and wire Vitest

**Files:**
- Modify: `web/package.json`
- Create: `web/vitest.config.ts`
- Create: `web/src/test/setup.ts`

- [ ] **Step 1: Add devDependencies and a `test` script**

Edit `web/package.json`. In `"scripts"`, add the `test` line:

```json
"scripts": {
  "dev": "vite",
  "build": "tsc && vite build",
  "preview": "vite preview",
  "test": "vitest run",
  "test:watch": "vitest"
}
```

In `"devDependencies"`, add three entries (keep existing entries, alphabetical order):

```json
"@testing-library/jest-dom": "^6.4.0",
"@testing-library/react": "^14.2.0",
"jsdom": "^24.0.0",
```

- [ ] **Step 2: Install**

Run: `cd web && npm install`
Expected: no errors; `package-lock.json` updates.

- [ ] **Step 3: Create the Vitest config**

Create `web/vitest.config.ts`:

```ts
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
```

- [ ] **Step 4: Create the setup file**

Create `web/src/test/setup.ts`:

```ts
import '@testing-library/jest-dom'
```

- [ ] **Step 5: Add a trivial sanity test to prove the config works**

Create `web/src/test/sanity.test.ts`:

```ts
import { describe, it, expect } from 'vitest'

describe('vitest', () => {
  it('runs', () => {
    expect(1 + 1).toBe(2)
  })
})
```

- [ ] **Step 6: Run the sanity test**

Run: `cd web && npm test`
Expected: `PASS  src/test/sanity.test.ts` and exit code 0.

- [ ] **Step 7: Delete the sanity test (served its purpose)**

Delete `web/src/test/sanity.test.ts`.

- [ ] **Step 8: Commit**

```bash
git add web/package.json web/package-lock.json web/vitest.config.ts web/src/test/setup.ts
git commit -m "build(p7): vitest + testing-library scaffolding for web tests"
```

---

## Phase 2 — LogTail component (TDD)

### Task 2: Add `eventsStreamURL` helper on api

**Files:**
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Add the helper**

Open `web/src/lib/api.ts`. Inside the `runs` object literal (current shape shown in the file), add one line *after* the existing `events` entry:

```ts
runs: {
  list: (params?: { limit?: number; schedule_id?: string }) => {
    const qs = new URLSearchParams()
    if (params?.limit) qs.set('limit', String(params.limit))
    if (params?.schedule_id) qs.set('schedule_id', params.schedule_id)
    return apiFetch<RunSummary[]>(`/api/runs?${qs}`)
  },
  get: (id: string) => apiFetch<RunDetail>(`/api/runs/${id}`),
  events: (id: string) => apiFetch<RunEvent[]>(`/api/runs/${id}/events`),
  eventsStreamURL: (id: string) => `/api/runs/${id}/events/stream`,
},
```

- [ ] **Step 2: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/api.ts
git commit -m "feat(p7): add api.runs.eventsStreamURL helper"
```

### Task 3: Failing test — LogTail opens and closes EventSource

**Files:**
- Create: `web/src/components/LogTail.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/components/LogTail.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import LogTail from './LogTail'

class MockEventSource {
  static instances: MockEventSource[] = []
  url: string
  onmessage: ((ev: MessageEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  listeners = new Map<string, (ev: MessageEvent) => void>()
  closed = false

  constructor(url: string) {
    this.url = url
    MockEventSource.instances.push(this)
  }
  addEventListener(type: string, fn: (ev: MessageEvent) => void) {
    this.listeners.set(type, fn)
  }
  close() {
    this.closed = true
  }
  emit(data: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(data) }))
  }
  emitDone() {
    this.listeners.get('done')?.(new MessageEvent('done', { data: '{}' }))
  }
}

beforeEach(() => {
  MockEventSource.instances = []
  vi.stubGlobal('EventSource', MockEventSource)
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('LogTail — streaming mode', () => {
  it('opens an EventSource to the stream URL when status is running', () => {
    render(<LogTail runId="abc" status="running" />)
    expect(MockEventSource.instances).toHaveLength(1)
    expect(MockEventSource.instances[0].url).toBe('/api/runs/abc/events/stream')
  })

  it('closes the EventSource on unmount', () => {
    const { unmount } = render(<LogTail runId="abc" status="running" />)
    const es = MockEventSource.instances[0]
    unmount()
    expect(es.closed).toBe(true)
  })
})
```

- [ ] **Step 2: Run test to confirm failure**

Run: `cd web && npm test -- LogTail`
Expected: FAIL with `Cannot find module './LogTail'` or similar.

### Task 4: Implement LogTail — minimal streaming

**Files:**
- Create: `web/src/components/LogTail.tsx`

- [ ] **Step 1: Implement the minimal component**

Create `web/src/components/LogTail.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import type { RunEvent, RunStatus } from '../lib/types'

type Props = {
  runId: string
  status: RunStatus
}

const TERMINAL: ReadonlySet<RunStatus> = new Set([
  'succeeded',
  'partial_failure',
  'failed',
])

export default function LogTail({ runId, status }: Props) {
  const [events, setEvents] = useState<RunEvent[]>([])

  useEffect(() => {
    if (TERMINAL.has(status)) return
    const es = new EventSource(api.runs.eventsStreamURL(runId))
    es.onmessage = ev => {
      try {
        const parsed = JSON.parse(ev.data) as RunEvent
        setEvents(prev => [...prev, parsed])
      } catch {
        // malformed line — ignore
      }
    }
    es.addEventListener('done', () => {
      es.close()
    })
    return () => {
      es.close()
    }
  }, [runId, status])

  return (
    <div
      role="log"
      className="mt-4 h-64 overflow-y-auto rounded bg-black/60 p-2 font-mono text-xs text-gray-300"
    >
      {events.length === 0 ? (
        <div className="text-gray-600">Waiting for events…</div>
      ) : (
        events.map(ev => <LogRow key={ev.id} ev={ev} />)
      )}
    </div>
  )
}

function LogRow({ ev }: { ev: RunEvent }) {
  const [expanded, setExpanded] = useState(false)
  return (
    <div
      onClick={() => setExpanded(v => !v)}
      className="cursor-pointer border-b border-gray-800/50 py-0.5 hover:bg-gray-800/30"
    >
      <span className="text-gray-600">{new Date(ev.ts).toLocaleTimeString()} </span>
      <span className={ev.level === 'error' ? 'text-red-400' : 'text-gray-400'}>
        {ev.level}
      </span>{' '}
      <span className="text-gray-200">{ev.event_type}</span>
      {expanded && (
        <pre className="mt-1 whitespace-pre-wrap break-all text-gray-500">
          {JSON.stringify(ev.payload_json, null, 2)}
        </pre>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Run tests — confirm the two existing tests pass**

Run: `cd web && npm test -- LogTail`
Expected: both "opens an EventSource" and "closes on unmount" PASS.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/LogTail.tsx web/src/components/LogTail.test.tsx
git commit -m "feat(p7): LogTail — minimal streaming"
```

### Task 5: Terminal status closes the stream

**Files:**
- Modify: `web/src/components/LogTail.test.tsx`
- Modify: `web/src/components/LogTail.tsx`

- [ ] **Step 1: Add failing test (append inside the existing `describe` block)**

Append to `LogTail.test.tsx` inside `describe('LogTail — streaming mode', ...)`:

```tsx
  it('does NOT open a stream when status is terminal on mount', () => {
    render(<LogTail runId="abc" status="succeeded" />)
    expect(MockEventSource.instances).toHaveLength(0)
  })

  it('closes the stream when status transitions to terminal', () => {
    const { rerender } = render(<LogTail runId="abc" status="running" />)
    const es = MockEventSource.instances[0]
    expect(es.closed).toBe(false)
    rerender(<LogTail runId="abc" status="succeeded" />)
    expect(es.closed).toBe(true)
  })
```

Also add a new describe for the server's `done` event:

```tsx
describe('LogTail — stream termination', () => {
  it("closes when server emits 'done' event", () => {
    render(<LogTail runId="abc" status="running" />)
    const es = MockEventSource.instances[0]
    es.emitDone()
    expect(es.closed).toBe(true)
  })
})
```

- [ ] **Step 2: Run — confirm the first new test fails (mount with terminal status still opens a stream today — it does not, because the existing code already guards with `TERMINAL.has(status)` → so that test will PASS immediately). Re-run to check which tests actually fail.**

Run: `cd web && npm test -- LogTail`
Expected (actual result): `mount-terminal` PASSES (guard exists); `transition` FAILS (the effect's cleanup runs on `status` change, so the old stream *is* closed — this test may also PASS); `'done' event closes` PASSES (close is called in the listener).

If all three pass on first run, skip to Step 4.

- [ ] **Step 3: Fix any failing test**

If the `transition` test failed, make sure the component's `useEffect` has both `runId` and `status` in its dependency array (it already does). No change needed.

- [ ] **Step 4: Run — confirm all pass**

Run: `cd web && npm test -- LogTail`
Expected: 5 passing.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/LogTail.test.tsx
git commit -m "test(p7): LogTail stream-termination cases"
```

### Task 6: Static mode — finished run on mount

**Files:**
- Modify: `web/src/components/LogTail.test.tsx`
- Modify: `web/src/components/LogTail.tsx`

- [ ] **Step 1: Write the failing test**

Append to `LogTail.test.tsx`:

```tsx
describe('LogTail — static mode', () => {
  it('fetches historical events when status is terminal on mount', async () => {
    const fetchSpy = vi.fn(async () => [
      { id: 1, run_id: 'abc', ts: new Date().toISOString(), level: 'info', event_type: 'llm.start', payload_json: {} },
      { id: 2, run_id: 'abc', ts: new Date().toISOString(), level: 'info', event_type: 'publish.slack.ok', payload_json: {} },
    ])
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      json: fetchSpy,
    })))

    const { findByText } = render(<LogTail runId="abc" status="succeeded" />)
    await findByText('llm.start')
    await findByText('publish.slack.ok')
    expect(MockEventSource.instances).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run — confirm failure**

Run: `cd web && npm test -- LogTail`
Expected: FAIL — the new test times out waiting for `llm.start` text, because static fetch isn't implemented yet.

- [ ] **Step 3: Add static fetch to the component**

Edit `web/src/components/LogTail.tsx`. Replace the `useEffect` with:

```tsx
  useEffect(() => {
    if (TERMINAL.has(status)) {
      let cancelled = false
      api.runs.events(runId).then(rows => {
        if (!cancelled) setEvents(rows)
      })
      return () => {
        cancelled = true
      }
    }
    const es = new EventSource(api.runs.eventsStreamURL(runId))
    es.onmessage = ev => {
      try {
        const parsed = JSON.parse(ev.data) as RunEvent
        setEvents(prev => [...prev, parsed])
      } catch {
        // malformed line — ignore
      }
    }
    es.addEventListener('done', () => {
      es.close()
    })
    return () => {
      es.close()
    }
  }, [runId, status])
```

- [ ] **Step 4: Run — confirm all pass**

Run: `cd web && npm test -- LogTail`
Expected: 6 passing.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/LogTail.tsx web/src/components/LogTail.test.tsx
git commit -m "feat(p7): LogTail — static mode for finished runs"
```

### Task 7: Sticky-to-bottom auto-scroll

**Files:**
- Modify: `web/src/components/LogTail.test.tsx`
- Modify: `web/src/components/LogTail.tsx`

- [ ] **Step 1: Write the failing test**

jsdom doesn't lay out, so we verify the scroll helper is *called*, not that pixels move. Append:

```tsx
describe('LogTail — auto-scroll', () => {
  it('scrolls to bottom on new event when sticky', () => {
    const scrollToSpy = vi.fn()
    // jsdom: override Element.prototype so our ref's scrollTo is captured
    Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
      value: scrollToSpy,
      configurable: true,
    })

    render(<LogTail runId="abc" status="running" />)
    const es = MockEventSource.instances[0]
    es.emit({
      id: 10,
      run_id: 'abc',
      ts: new Date().toISOString(),
      level: 'info',
      event_type: 'llm.chunk',
      payload_json: {},
    })
    // One frame later React has flushed and the effect has run
    return Promise.resolve().then(() => {
      expect(scrollToSpy).toHaveBeenCalled()
    })
  })
})
```

- [ ] **Step 2: Run — confirm failure**

Run: `cd web && npm test -- LogTail`
Expected: FAIL — `scrollToSpy` was not called.

- [ ] **Step 3: Implement sticky-to-bottom**

Edit `web/src/components/LogTail.tsx`. Add the ref, the scroll handler, and a scroll effect. The final component body:

```tsx
import { useEffect, useRef, useState } from 'react'
import { api } from '../lib/api'
import type { RunEvent, RunStatus } from '../lib/types'

type Props = {
  runId: string
  status: RunStatus
}

const TERMINAL: ReadonlySet<RunStatus> = new Set([
  'succeeded',
  'partial_failure',
  'failed',
])
const STICKY_THRESHOLD_PX = 50

export default function LogTail({ runId, status }: Props) {
  const [events, setEvents] = useState<RunEvent[]>([])
  const [sticky, setSticky] = useState(true)
  const scrollRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (TERMINAL.has(status)) {
      let cancelled = false
      api.runs.events(runId).then(rows => {
        if (!cancelled) setEvents(rows)
      })
      return () => {
        cancelled = true
      }
    }
    const es = new EventSource(api.runs.eventsStreamURL(runId))
    es.onmessage = ev => {
      try {
        const parsed = JSON.parse(ev.data) as RunEvent
        setEvents(prev => [...prev, parsed])
      } catch {
        // malformed line — ignore
      }
    }
    es.addEventListener('done', () => {
      es.close()
    })
    return () => {
      es.close()
    }
  }, [runId, status])

  useEffect(() => {
    if (!sticky) return
    const el = scrollRef.current
    if (!el) return
    el.scrollTo?.({ top: el.scrollHeight })
  }, [events, sticky])

  function handleScroll() {
    const el = scrollRef.current
    if (!el) return
    const atBottom =
      el.scrollHeight - el.scrollTop - el.clientHeight < STICKY_THRESHOLD_PX
    setSticky(atBottom)
  }

  return (
    <div
      ref={scrollRef}
      onScroll={handleScroll}
      role="log"
      className="mt-4 h-64 overflow-y-auto rounded bg-black/60 p-2 font-mono text-xs text-gray-300"
    >
      {events.length === 0 ? (
        <div className="text-gray-600">Waiting for events…</div>
      ) : (
        events.map(ev => <LogRow key={ev.id} ev={ev} />)
      )}
    </div>
  )
}

function LogRow({ ev }: { ev: RunEvent }) {
  const [expanded, setExpanded] = useState(false)
  return (
    <div
      onClick={() => setExpanded(v => !v)}
      className="cursor-pointer border-b border-gray-800/50 py-0.5 hover:bg-gray-800/30"
    >
      <span className="text-gray-600">{new Date(ev.ts).toLocaleTimeString()} </span>
      <span className={ev.level === 'error' ? 'text-red-400' : 'text-gray-400'}>
        {ev.level}
      </span>{' '}
      <span className="text-gray-200">{ev.event_type}</span>
      {expanded && (
        <pre className="mt-1 whitespace-pre-wrap break-all text-gray-500">
          {JSON.stringify(ev.payload_json, null, 2)}
        </pre>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run — confirm all pass**

Run: `cd web && npm test -- LogTail`
Expected: 7 passing.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/LogTail.tsx web/src/components/LogTail.test.tsx
git commit -m "feat(p7): LogTail — sticky-to-bottom auto-scroll"
```

### Task 8: Reconnect cap

**Files:**
- Modify: `web/src/components/LogTail.test.tsx`
- Modify: `web/src/components/LogTail.tsx`

- [ ] **Step 1: Extend the mock to fire errors**

Edit `web/src/components/LogTail.test.tsx`. Add a helper method on `MockEventSource`:

```tsx
  emitError() {
    this.onerror?.(new Event('error'))
  }
```

- [ ] **Step 2: Write the failing test**

Append:

```tsx
describe('LogTail — reconnect cap', () => {
  it('shows "connection lost" after 5 consecutive errors', async () => {
    const { findByText, queryByText } = render(
      <LogTail runId="abc" status="running" />
    )
    const es = MockEventSource.instances[0]

    for (let i = 0; i < 4; i++) es.emitError()
    expect(queryByText(/connection lost/i)).toBeNull()
    expect(es.closed).toBe(false)

    es.emitError() // 5th
    await findByText(/connection lost/i)
    expect(es.closed).toBe(true)
  })
})
```

- [ ] **Step 3: Run — confirm failure**

Run: `cd web && npm test -- LogTail`
Expected: FAIL — `connection lost` text not found, `es.closed` is false.

- [ ] **Step 4: Implement the cap**

Edit `web/src/components/LogTail.tsx`:

1. Add `const RETRY_CAP = 5` near the top.
2. Add state: `const [lost, setLost] = useState(false)` and a `retryRef = useRef(0)`.
3. In the streaming branch of the effect, add `es.onerror = () => { retryRef.current++; if (retryRef.current >= RETRY_CAP) { es.close(); setLost(true); } }`.
4. Reset `retryRef.current = 0` at the top of the streaming branch (one run-level retry budget per mount).
5. In the render, before the events list, conditionally render a notice when `lost` is true:

```tsx
      {lost && (
        <div className="mb-1 text-red-400">
          connection lost — reload to retry
        </div>
      )}
```

Full updated effect (replace the existing streaming branch):

```tsx
    retryRef.current = 0
    const es = new EventSource(api.runs.eventsStreamURL(runId))
    es.onmessage = ev => {
      try {
        const parsed = JSON.parse(ev.data) as RunEvent
        setEvents(prev => [...prev, parsed])
      } catch {
        // malformed line — ignore
      }
    }
    es.onerror = () => {
      retryRef.current++
      if (retryRef.current >= RETRY_CAP) {
        es.close()
        setLost(true)
      }
    }
    es.addEventListener('done', () => {
      es.close()
    })
    return () => {
      es.close()
    }
```

- [ ] **Step 5: Run — confirm all pass**

Run: `cd web && npm test -- LogTail`
Expected: 8 passing.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/LogTail.tsx web/src/components/LogTail.test.tsx
git commit -m "feat(p7): LogTail — reconnect cap surfaces connection-lost notice"
```

---

## Phase 3 — Integration

### Task 9: Wire LogTail into the Runs detail drawer

**Files:**
- Modify: `web/src/pages/Runs.tsx`

- [ ] **Step 1: Import LogTail and replace the inline event list**

Open `web/src/pages/Runs.tsx`. Add the import:

```tsx
import LogTail from '../components/LogTail'
```

Remove the polling `useQuery` for `run-events` and the `events.map(...)` block inside `RunDetail`. Replace with a single `<LogTail>` call. The updated `RunDetail` body:

```tsx
function RunDetail({ runId, onClose }: { runId: string; onClose: () => void }) {
  const { data: run } = useQuery({
    queryKey: ['run', runId],
    queryFn: () => api.runs.get(runId),
    refetchInterval: query => {
      const s = query.state.data?.status
      return s === 'pending' || s === 'running' ? 2000 : false
    },
  })

  return (
    <div className="w-96 shrink-0 border-l border-gray-800 pl-6">
      <div className="flex items-center justify-between mb-4">
        <h2 className="font-semibold text-white">Run detail</h2>
        <button onClick={onClose} className="text-gray-500 hover:text-white">✕</button>
      </div>
      {run && (
        <>
          <div className="space-y-2 text-sm mb-4">
            <div>
              <span className="text-gray-500">Status: </span>
              <RunStatusBadge status={run.status} />
            </div>
            {run.tokens_in != null && (
              <div className="text-gray-400">
                Tokens: {run.tokens_in} in / {run.tokens_out} out
              </div>
            )}
            {run.cost_cents != null && (
              <div className="text-gray-400">
                Cost: ${(run.cost_cents / 100).toFixed(4)}
              </div>
            )}
            {run.error_msg && (
              <div className="text-red-400 text-xs">{run.error_msg}</div>
            )}
          </div>
          <LogTail runId={run.id} status={run.status} />
        </>
      )}
    </div>
  )
}
```

Remove the now-unused `useState`-for-events bookkeeping (there was none — only the query) and any unused imports (`useQuery` is still used by the `run` query, so keep it; the `events` query and its `.map` go away).

- [ ] **Step 2: Typecheck**

Run: `cd web && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Build**

Run: `cd web && npm run build`
Expected: build succeeds.

- [ ] **Step 4: Run the unit tests once more**

Run: `cd web && npm test`
Expected: 8 passing (LogTail suite). No other tests exist yet.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/Runs.tsx
git commit -m "feat(p7): use LogTail in Runs detail drawer"
```

### Task 10: Manual browser verification

**Files:**
- None (verification only)

- [ ] **Step 1: Start the local dev harness**

Run (from repo root): `make dev` (brings up Postgres + cronfoundry in docker-compose).

- [ ] **Step 2: Start the Vite dev server in another terminal**

Run: `cd web && npm run dev`
Expected: dev server listening on `http://localhost:5173` with `/api` proxied to `localhost:8080`.

- [ ] **Step 3: Log in**

Open `http://localhost:5173`. Follow the OAuth flow. Land on the dashboard.

- [ ] **Step 4: Trigger a run and open its detail**

From the dashboard, click "Run now" on any schedule (or start a run via the CLI if no schedule is ready). Navigate to **Runs**, click the newest row.

- [ ] **Step 5: Confirm the log panel streams**

Expected during an in-flight run:
- New rows appear in the log panel every ~2 seconds (server polls DB at 2s).
- Auto-scrolls to bottom as new rows arrive.
- Scroll up → new rows arrive but view stays put.
- Scroll back to bottom → auto-scroll resumes.

When the run finishes, `done` event closes the stream, no further rows appear, no browser-console `EventSource` errors.

- [ ] **Step 6: Confirm static mode on a finished run**

Close and re-open the detail drawer for the same (now-finished) run. The log panel should populate via `/api/runs/{id}/events` — no open `EventSource` in the Network tab.

- [ ] **Step 7: No commit** (verification only)

If any step fails, go back to the relevant component task and fix. Do not proceed until all six steps pass.

---

## Phase 4 — Docs refresh

### Task 11: README — status, roadmap, architecture package list

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the Status line**

Replace the current Status paragraph (near the top; currently begins "**Status:** `P2 — service layer complete`"):

```markdown
**Status:** `MVP shipped — deployable to Azure`. Includes always-on scheduler,
GitHub App sync, `/internal` HTTP API, subprocess + Container Apps Jobs runner
dispatch, React operator UI, and a one-command Bicep deploy. See
[`docs/superpowers/specs/2026-04-19-cronfoundry-design.md`](docs/superpowers/specs/2026-04-19-cronfoundry-design.md)
for the design and [`docs/guides/smoke-test-mvp-azure.md`](docs/guides/smoke-test-mvp-azure.md)
for the Azure runbook.
```

- [ ] **Step 2: Rewrite the Roadmap section**

Find the `## Roadmap` section near the bottom. Replace the entire section with:

```markdown
## Roadmap

- **MVP** (this release) — Core runner, scheduler, GitHub sync, Key Vault,
  React UI, Azure Bicep deploy. ✅
- **Deferred** — see the "Deferred" section of the
  [design spec](docs/superpowers/specs/2026-04-19-cronfoundry-design.md) for
  the ordered backlog (MCP tool support, Copilot Enterprise provider,
  auto-pause on consecutive failures, etc.).
```

- [ ] **Step 3: Refresh the Architecture package list**

Find the `/internal/` tree block under `## Architecture`. The current list stops at P1 packages. Replace the list with the current set (verify against `ls internal/` first if unsure):

```
cronfoundry/
├── cmd/
│   ├── cronfoundry/              # server + admin CLI (cobra)
│   └── runner/                   # one-shot per-fire runner
└── internal/
    ├── api/                      # /internal HTTP endpoints (runner-facing)
    ├── cloud/                    # Azure Container Apps Jobs dispatcher
    ├── config/                   # cronfoundry.yaml + SKILL.md parsers
    ├── db/                       # pgx + goose migrations + sqlc queries
    ├── github/                   # App JWT, install tokens, clone/commit
    ├── githubtest/               # test fixtures for github/
    ├── llm/                      # OpenAI / Anthropic / Azure Foundry
    ├── memory/                   # <memory>...</memory> parser
    ├── publish/                  # github-issue / slack / discord / teams
    ├── redact/                   # secret-value scrubber for logs
    ├── runner/                   # orchestration (load → LLM → publish)
    ├── scheduler/                # cron tick loop + overlap + sweep
    ├── secrets/                  # env-based secret resolver (runner-local)
    ├── secretstore/              # Azure Key Vault wrapper (server-side)
    ├── sync/                     # GitHub repo → skill/schedule sync
    ├── template/                 # destination-template renderer
    ├── testdb/                   # testcontainers Postgres boot helper
    ├── token/                    # GitHub installation token cache
    ├── webapi/                   # /api handlers for the React UI
    └── writeback/                # go-git commit + push
```

- [ ] **Step 4: Typo/consistency pass**

Run: `grep -n "P[0-9]" README.md`
Review each match: every remaining reference to phases should either be under the Roadmap section (where "MVP" subsumes P1–P5) or gone. Remove stale phase mentions from other sections if any.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs(p7): README — MVP shipped status + architecture refresh"
```

### Task 12: GitHub Pages site — status copy

**Files:**
- Modify: `docs/index.html`

- [ ] **Step 1: Locate stale status language**

Run: `grep -n -i "coming soon\|in progress\|roadmap\|p[0-9]" docs/index.html`
Record the line numbers. Common candidates: hero subline, a "status" or "roadmap" section, CTA copy.

- [ ] **Step 2: Edit each stale line**

For each match, apply one of:

- Replace "Coming soon" → "Shipped."
- Replace "In progress" → "Shipped."
- Replace phase-specific roadmap bullets → a single "MVP shipped — deployable to Azure today" line with a link to `https://github.com/gambtho/cronfoundry`.

Keep nav, hero headline ("Scheduled LLM skills. In git. For your whole team."), visual design, terminal animation, and meta tags (`<title>`, `<meta description>`, OG tags) untouched.

- [ ] **Step 3: Add a one-line self-host beat**

In the hero or a features section, add one line (follow neighbouring styling):

```html
<p class="mt-2 text-sm text-gray-400">
  Self-host on Azure in one afternoon —
  <a href="https://github.com/gambtho/cronfoundry/blob/main/docs/guides/smoke-test-mvp-azure.md"
     class="text-brand hover:underline">follow the runbook →</a>
</p>
```

- [ ] **Step 4: Open the page locally**

Run: `cd docs && python3 -m http.server 8000`
Open `http://localhost:8000` in a browser. Confirm:
- Hero renders.
- Terminal animation plays.
- Status copy reads "Shipped" (no "coming soon" remnants).
- The runbook link is present and has the correct href.

- [ ] **Step 5: Stop the server, commit**

```bash
git add docs/index.html
git commit -m "docs(p7): GitHub Pages — MVP shipped status"
```

### Task 13: Spot-check other guides

**Files:**
- Modify (only if broken): `docs/guides/smoke-test-p2.md`, `docs/guides/smoke-test-p4.md`, `docs/guides/deploy-azure.md`, `docs/guides/observability.md`

- [ ] **Step 1: Grep for stale references**

Run: `grep -rn "P[0-9]\b\|coming soon\|will be\|TBD" docs/guides/`

- [ ] **Step 2: For each match, decide**

- If the claim is still correct (e.g., "P4 introduces Container Apps Jobs" — historically true) → leave alone.
- If the claim is wrong (e.g., a feature described as "will ship in a later phase" that is now shipped) → fix inline.

Do **not** re-polish or rewrite guides that aren't wrong. Out-of-scope work is out of scope.

- [ ] **Step 3: If no changes were needed, skip the commit. Otherwise commit only the changed files.**

```bash
git add docs/guides/<only-the-files-you-changed>
git commit -m "docs(p7): fix stale references in guides"
```

---

## Phase 5 — Azure smoke runbook

### Task 14: Write the runbook skeleton

**Files:**
- Create: `docs/guides/smoke-test-mvp-azure.md`

- [ ] **Step 1: Draft the runbook**

Create `docs/guides/smoke-test-mvp-azure.md`:

````markdown
# Smoke Test — MVP on Azure

End-to-end verification that a fresh CronFoundry deployment on Azure can fire
a scheduled skill, publish to Slack + GitHub, commit a memory update back to
the skill repo, and write audit rows for every mutating action.

Executed once by the author on a fresh subscription. Any step that fails
becomes a fix (code or doc), and the runbook is re-run. The committed version
is the one that worked end to end.

**Depends on:** P6 merged (`docs/superpowers/plans/2026-04-20-p6-mvp-gaps.md`)
for the audit-verification and push-webhook steps.

---

## 1. Prerequisites

- An Azure subscription with Contributor rights.
- `az` CLI ≥ 2.60 and Bicep CLI ≥ 0.26. Verify: `az --version`.
- A GitHub account that can register a new GitHub App.
- **One** LLM key from OpenAI, Anthropic, or Azure AI Foundry.
- A Slack **Incoming Webhook URL** for any channel. Create one at
  https://api.slack.com/apps.
- Two GitHub repos under the same owner:
  1. A **skill repo** containing `cronfoundry.yaml` + one `SKILL.md`. You can
     copy `testdata/` from this repo into a new private repo to get going.
  2. A **reports repo** where the `github-issue` destination will file.
- Local clone of this repo for the admin CLI.

## 2. Register the GitHub App

1. GitHub → Settings → Developer settings → GitHub Apps → **New GitHub App**.
2. **Homepage URL:** anything (e.g., `https://<your-api-hostname>`). You'll fill
   the real hostname after the Bicep deploy in step 3.
3. **Callback URL:** `https://<your-api-hostname>/oauth/callback`
4. **Webhook URL:** `https://<your-api-hostname>/webhook/github`  
   **Webhook secret:** generate a long random string; save for step 4.
5. **Permissions:**
   - Repository: Contents (R+W), Issues (W), Metadata (R).
   - Account: Email (R).
6. **Subscribe to events:** Push.
7. Save. Generate + download the **private key** (`.pem`). Note the **App ID**
   and **Client ID / Client Secret**.
8. **Install the App** on your two repos (skill + reports).

You'll come back after step 3 to update the three URLs with the real hostname.

## 3. Deploy via Bicep

```bash
cp deploy/params.example.json deploy/params.p7smoke.json
# Edit deploy/params.p7smoke.json — set:
#   envName:           "p7smoke"
#   location:          "eastus" (or your region)
#   adminLogins:       ["<your-github-login>"]
#   containerImageTag: "latest" (or a specific release tag)

az deployment sub create \
  --location eastus \
  --template-file deploy/main.bicep \
  --parameters @deploy/params.p7smoke.json
```

After ~10 minutes, the deployment completes. Grab the API hostname:

```bash
az containerapp show \
  --resource-group rg-cronfoundry-p7smoke \
  --name api \
  --query properties.configuration.ingress.fqdn -o tsv
```

Go back to the GitHub App settings and replace `<your-api-hostname>` in
Homepage URL, Callback URL, and Webhook URL with this FQDN. Save.

## 4. First-boot config (admin CLI)

All of the following run from a local shell with the master key exported
(see README § Quick start; the Bicep deploy prints it once at the end).

```bash
export CRONFOUNDRY_MASTER_KEY='<from deploy output>'
export CRONFOUNDRY_DATABASE_URL='<from deploy output>'

# Connect the skill repo. Replace with your install ID and coords.
./cronfoundry admin connect-repo <owner>/<skill-repo> \
  --installation-id <from GitHub App installation page>

# Set three secrets.
echo -n '<openai/anthropic/azure key>'  | ./cronfoundry admin set-secret llm_key
echo -n '<slack webhook URL>'           | ./cronfoundry admin set-secret slack_webhook
echo -n '<github webhook secret>'       | ./cronfoundry admin set-secret github_webhook_secret
```

Also set the webhook secret as an env var on the API Container App (the
webhook endpoint reads it from `CRONFOUNDRY_GITHUB_WEBHOOK_SECRET`):

```bash
az containerapp update \
  --resource-group rg-cronfoundry-p7smoke \
  --name api \
  --set-env-vars CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=<same value>
```

## 5. Land a skill

In your skill repo's `cronfoundry.yaml`, define one schedule firing every 5
minutes:

```yaml
version: 1
skills:
  - path: skills/smoke
    schedules:
      - name: every-5
        cron: "*/5 * * * *"
        timezone: UTC
        overlap_policy: skip
        timeout_sec: 300
        provider: openai        # or anthropic / azure-foundry
        model: gpt-4o-mini
        destinations:
          - slack:
              secret: slack_webhook
              text: "{{ output.truncated 35000 }}"
          - github-issue:
              repo: <owner>/<reports-repo>
              title: "smoke — {{ run.date }}"
              labels: [smoke]
        writeback:
          enabled: true
          path: memory.md
          mode: append
```

And `skills/smoke/SKILL.md`:

```markdown
---
name: smoke
description: Proves a run end-to-end
max_tokens: 500
---
Write one short paragraph confirming this pipeline works.
End with:

<memory>
run at {{ run.started_at }}
</memory>
```

Commit and push. The push webhook re-syncs the schedule within seconds.

## 6. Observe the first fire

1. Open `https://<fqdn>/`. Log in via GitHub. (You should already be
   allowlisted from the bootstrap admin list.)
2. Dashboard shows the new `every-5` schedule.
3. Wait up to 5 minutes for the first natural fire — or click **Run now**.
4. Go to **Runs**, click the newest row.
5. Confirm the **log panel streams** with row levels `info/warn/error` and
   event types (`llm.start`, `llm.chunk.batched`, `publish.slack.ok`,
   `publish.github-issue.ok`, `writeback.commit.ok`). Status transitions to
   `succeeded`.

## 7. Verify the three side effects

- **Slack:** message lands in the configured channel with the skill output.
- **GitHub issue:** a new issue exists in the reports repo, titled
  `smoke — YYYY-MM-DD`, labeled `smoke`.
- **Writeback commit:** the skill repo's `memory.md` has a new line; commit
  author is `cronfoundry[bot]` with message
  `chore(cronfoundry): update memory.md from run <uuid>`.

## 8. Verify the audit log

Navigate to **Audit** in the sidebar (shipped in P6c). Confirm rows are
present for the session you just walked through:

| Action | Target |
|---|---|
| `auth.login` | your github login |
| `repo.connect` | `<owner>/<skill-repo>` |
| `secret.create` | `llm_key` |
| `secret.create` | `slack_webhook` |
| `secret.create` | `github_webhook_secret` |
| `schedule.run_now` | `every-5` (only if you clicked **Run now**) |

If any row is missing, file it as a P6c fix and do not mark the smoke passed.

## 9. Teardown

```bash
az group delete --name rg-cronfoundry-p7smoke --yes --no-wait
```

Revoke the GitHub App installation and delete the App registration once the
resource group is gone.

---

## Pass/fail checklist

- [ ] One `succeeded` run visible in the dashboard.
- [ ] Slack message present.
- [ ] GitHub issue filed in the reports repo.
- [ ] `memory.md` commit authored by `cronfoundry[bot]`.
- [ ] Audit log contains login + repo-connect + secret-create rows.

If every box is checked, MVP is shipped. Otherwise, every unchecked box
becomes a fix in code or docs and the runbook re-runs.
````

- [ ] **Step 2: Commit the skeleton**

```bash
git add docs/guides/smoke-test-mvp-azure.md
git commit -m "docs(p7): Azure MVP smoke runbook (draft — pre-execution)"
```

### Task 15: Execute the runbook (operator task)

**Files:**
- Modify (as needed): `docs/guides/smoke-test-mvp-azure.md` and any code/doc the walkthrough catches

> **Note for agentic workers:** this task requires a human operator with an
> Azure subscription, a GitHub account, and a Slack workspace. An agent
> cannot complete it end to end. Produce a PR with Tasks 1–14, then leave
> this task for the human reviewer to execute against the merged branch (or
> a deploy from the branch). The human's commits against the runbook finish
> the phase.

- [ ] **Step 1: Human operator walks the runbook top-to-bottom on a fresh
  Azure sub.**

- [ ] **Step 2: At each failing step, identify whether the fix belongs in code
  or docs. Make the fix. Re-run from that step.**

- [ ] **Step 3: Keep iterating until the pass/fail checklist is clean.**

- [ ] **Step 4: Commit any runbook edits captured during execution.**

```bash
git add docs/guides/smoke-test-mvp-azure.md
git commit -m "docs(p7): runbook fixes from live Azure execution"
```

- [ ] **Step 5: Open/merge the PR. MVP is shipped.**

---

## Self-review

**Spec coverage**
- LogTail (streaming + static + sticky-scroll + reconnect cap + Runs integration) → Tasks 3–9 ✓
- Vitest scaffolding → Task 1 ✓
- README roadmap/status/architecture → Task 11 ✓
- GitHub Pages status refresh → Task 12 ✓
- Spot-check other guides → Task 13 ✓
- Azure smoke runbook (write + execute) → Tasks 14–15 ✓
- Manual browser verification of LogTail → Task 10 ✓

**Placeholder scan**
- No "TBD," no "appropriate error handling," no "etc.," no "similar to Task N."
- Task 15 is explicitly gated on a human operator, with the rationale stated
  inline — that is a real-world dependency, not a placeholder.

**Type consistency**
- `RunStatus`, `RunEvent`, `RunSummary`, `RunDetail` used as defined in
  `web/src/lib/types.ts`. `TERMINAL` set covers `succeeded | partial_failure | failed`
  — matches `RunStatus` terminal subset.
- `api.runs.eventsStreamURL(id)` added in Task 2 and consumed verbatim in
  Tasks 4, 6, 8.
- `MockEventSource` stays consistent across Tasks 3, 5, 6, 7, 8: adds
  `listeners` map, `emit`, `emitDone`, `emitError`. Each task adds only what
  its test needs.
- Component exports a default function `LogTail`; every consumer uses
  `import LogTail from '../components/LogTail'`.
