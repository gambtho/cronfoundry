# Run timeline phases — design

## Problem

The Run detail page mocks show a segmented progress bar
(boot → secrets → exec → publish, capped by fail) so an operator can see
at a glance where time was spent and where a run died. The UI is built
but blocked: the runner emits MCP-flavored events
(`mcp.server.start.ok`, `mcp.tool.call.ok`, etc.) with no notion of the
coarser lifecycle phase the bar needs.

## Approach

Emit explicit phase-boundary events from the runner. The
`run_events` table is unchanged. The existing
`GET /runs/{id}/events` endpoint and SSE stream carry the new event
type alongside everything else. The frontend reduces boundary events
into segments client-side.

This keeps the schema stable, puts the source of truth in the runner
(where phase transitions actually happen), and lets the timeline
update live through the existing SSE stream.

## Phase taxonomy

Five phases, terminal-capped:

| Phase     | Enters when                                                        |
| --------- | ------------------------------------------------------------------ |
| `boot`    | First runner emission, before any other work.                      |
| `secrets` | After boot, before secret resolution.                              |
| `exec`    | After secrets, MCP turn loop running.                              |
| `publish` | After exec returns a result, before destinations are dispatched.   |
| `fail`    | Terminal cap. Runner emits this instead of advancing on any error. |

Rules:

- Phases are entered in order on the happy path. None are skipped —
  if a phase has no work (e.g. no secrets to resolve), it still gets
  an `enter` and the segment is just very short.
- `fail` is a terminal phase. The runner emits it with a `prev` field
  naming the phase that was active so the UI can paint that segment
  red and cap the bar there.
- The terminal status (`succeeded`/`failed`/`canceled`) lives on the
  `runs` row. The timeline doesn't need a separate "done" event;
  the last `phase.enter` plus the run's `finished_at` bound the final
  segment.

## Event shape

New event type: `phase.enter`.

Payload:

```json
{ "phase": "secrets" }
```

On failure:

```json
{ "phase": "fail", "prev": "exec" }
```

`prev` is required when `phase=="fail"` and omitted otherwise.
`prev` MUST be one of `boot`/`secrets`/`exec`/`publish`.

## Runner changes

`internal/runner/runner.go`:

- Add an `EventPhaseEnter EventType = "phase.enter"` constant
  alongside the existing MCP event types.
- Add an unexported helper `enterPhase(phase string)` that calls
  `deps.EventSink` with the boundary event, used at every transition.
- Insert calls at the four happy-path boundaries (boot → secrets →
  exec → publish). The exact call sites land in the implementation
  plan; conceptually they sit at function entry for each stage.
- The error path emits `phase.enter` with `{"phase":"fail","prev":...}`
  before returning the failed `RunResult`. `prev` is whichever phase
  was last entered — track it in a local variable in `Run`.

The boundary helper is internal; nothing else should call it.

## Persistence

The runner's `EventSink` already feeds the existing pipeline that
writes events into `run_events` (see `internal/api/events.go` and
the runner→API event flow). `phase.enter` rides that path with no
changes — it serializes the same as any other event, with
`payload_json` carrying the `{phase, prev?}` object.

No migration. No new columns. No new endpoints.

## API

`GET /runs/{id}/events` and the SSE stream return `phase.enter`
events interleaved with the rest, same DTO as today
(`event_type: "phase.enter"`, `payload_json` per above). The
frontend filter is `events.filter(e => e.event_type === "phase.enter")`.

## Frontend reduce

Pseudocode for the timeline component:

```ts
const boundaries = events
  .filter(e => e.event_type === 'phase.enter')
  .map(e => ({ ts: e.ts, ...e.payload_json }))

// Pair consecutive boundaries into segments.
// The last segment ends at run.finished_at if terminal, else "now".
const segments = boundaries.map((b, i) => ({
  phase:    b.phase,
  start:    b.ts,
  end:      boundaries[i + 1]?.ts
              ?? run.finished_at
              ?? new Date().toISOString(),
  failed:   b.phase === 'fail',
  prevOnFail: b.prev,
}))
```

The `fail` segment has zero width; the UI paints the *previous*
segment red instead and caps the bar after it.

## Backward compatibility

Old runs (no `phase.enter` events) render as a single un-segmented
bar — the existing component already handles "no timeline data"
gracefully. The frontend treats absence as "old run, hide
segments", not as an error.

## Testing

- **Runner unit tests**: assert `phase.enter` events are emitted in
  order on the happy path, and that the failure path emits
  `{"phase":"fail","prev":"<X>"}` with the correct `prev`.
- **Frontend reducer test**: feed a known sequence of events,
  verify the produced segments (including the fail-cap case and the
  unfinished-run case where the last segment runs to "now").
- **No new integration tests required** — the events flow through
  the existing pipe.

## Out of scope

- Phase durations as first-class API fields (consumer can compute).
- A dedicated `/timeline` endpoint (rejected during brainstorm —
  duplicates state, doesn't stream).
- Phase tags on the existing MCP events (rejected — boundaries are
  enough for the UI we're building).
- Visualizing sub-phases inside `exec` (per-tool-call breakdown is
  a separate, later question if anyone asks).
