import { cn } from '../../lib/cn'
import type { RunStatus } from '../../lib/types'

/**
 * Pill — small inline status indicator with a glowing dot.
 *
 * Variants:
 *   - ok       : healthy / success (green)
 *   - fail     : error / failed run (red)
 *   - run      : currently running (amber, pulsing)
 *   - skip     : skipped / paused (muted)
 *   - amber    : warning (e.g. expiring secret)
 *   - partial  : partial failure (amber, distinct from full fail)
 *
 * Convenience: pass a `RunStatus` via `fromRunStatus` to map directly.
 */
export type PillVariant = 'ok' | 'fail' | 'run' | 'skip' | 'amber' | 'partial'

interface PillProps {
  variant: PillVariant
  children?: React.ReactNode
  className?: string
}

const VARIANT: Record<PillVariant, { dot: string; text: string }> = {
  ok: {
    dot: 'bg-accent-green shadow-[0_0_5px_rgba(61,220,132,0.6)]',
    text: 'text-accent-green',
  },
  fail: {
    dot: 'bg-accent-red shadow-[0_0_6px_var(--red)]',
    text: 'text-accent-red',
  },
  run: {
    dot: 'bg-accent-amber animate-pulse',
    text: 'text-accent-amber',
  },
  skip: {
    dot: 'bg-ink-3',
    text: 'text-ink-3',
  },
  amber: {
    dot: 'bg-accent-amber shadow-[0_0_5px_rgba(232,179,57,0.4)]',
    text: 'text-accent-amber',
  },
  partial: {
    dot: 'bg-accent-amber shadow-[0_0_5px_rgba(232,179,57,0.4)]',
    text: 'text-accent-amber',
  },
}

export function Pill({ variant, children, className }: PillProps) {
  const { dot, text } = VARIANT[variant]
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 font-mono text-[10px] font-medium uppercase tracking-[0.08em]',
        text,
        className,
      )}
    >
      <span className={cn('h-1.5 w-1.5 rounded-full', dot)} />
      {children}
    </span>
  )
}

/** Map a backend RunStatus to a Pill variant + label. */
export function pillFromRunStatus(status: RunStatus): {
  variant: PillVariant
  label: string
} {
  switch (status) {
    case 'succeeded':
      return { variant: 'ok', label: 'Success' }
    case 'failed':
      return { variant: 'fail', label: 'Failed' }
    case 'running':
      return { variant: 'run', label: 'Running' }
    case 'pending':
      return { variant: 'skip', label: 'Pending' }
    case 'partial_failure':
      return { variant: 'partial', label: 'Partial' }
  }
}
