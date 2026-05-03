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
    const end = next
      ? next.ts
      : (runFinishedAt ?? nowISO)
    const failed = next?.phase === 'fail' && next.prev === b.phase
    segments.push({ phase: b.phase, start: b.ts, end, failed })
  }
  return segments
}
