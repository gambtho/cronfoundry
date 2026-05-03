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
