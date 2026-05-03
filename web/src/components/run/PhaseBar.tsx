import { reduceTimeline, type TimelineSegment } from '../../lib/timeline'
import type { RunEvent } from '../../lib/types'

type Props = {
  events: RunEvent[]
  finishedAt: string | null
}

/**
 * Segmented timeline of a run's lifecycle: boot → secrets → exec → publish,
 * with a fail cap that paints the active segment red.
 *
 * The bar renders nothing when no phase.enter events are present (old runs,
 * or runs that haven't reached "boot" yet), so it's safe to mount
 * unconditionally.
 */
export function PhaseBar({ events, finishedAt }: Props) {
  const segments = reduceTimeline(events, finishedAt)
  if (segments.length === 0) return null

  const total = segments.reduce(
    (n, s) => n + Math.max(1, Date.parse(s.end) - Date.parse(s.start)),
    0,
  )

  return (
    <div
      role="img"
      aria-label="run phase timeline"
      className="flex h-2 w-full overflow-hidden rounded bg-rule"
    >
      {segments.map((s, i) => {
        const dur = Math.max(1, Date.parse(s.end) - Date.parse(s.start))
        const pct = (dur / total) * 100
        const tone = s.failed ? 'bg-red-500' : phaseColor(s.phase)
        return (
          <div
            key={i}
            title={`${s.phase}${s.failed ? ' (failed)' : ''} · ${(dur / 1000).toFixed(1)}s`}
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
