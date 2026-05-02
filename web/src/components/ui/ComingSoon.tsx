import { cn } from '../../lib/cn'

/**
 * ComingSoon — wraps a card-shaped block to signal that the data is
 * not yet wired up. We deliberately avoid faking numbers; placeholder
 * UI exists only so the layout you see in design reviews matches the
 * page once the API ships.
 *
 * Usage:
 *   <ComingSoon label="Provider health">
 *     <KV>...static example shape...</KV>
 *   </ComingSoon>
 */
interface ComingSoonProps {
  label?: string
  children: React.ReactNode
  className?: string
}

export function ComingSoon({
  label = 'Coming soon',
  children,
  className,
}: ComingSoonProps) {
  return (
    <div className={cn('relative', className)}>
      {/* Faint chevron stripes communicate "preview" without screaming.
          Pointer-events:none so the underlying static example stays
          inspectable but unclickable. */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 z-10 rounded bg-[repeating-linear-gradient(45deg,transparent_0_8px,rgba(255,255,255,0.018)_8px_16px)]"
      />
      <div className="pointer-events-none absolute right-2 top-2 z-20 rounded border border-rule-2 bg-bg-2 px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.14em] text-ink-3">
        {label}
      </div>
      <div className="opacity-50">{children}</div>
    </div>
  )
}
