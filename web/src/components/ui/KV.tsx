import { cn } from '../../lib/cn'

/**
 * KV — definition-list primitive used in right-rail context cards.
 * Two-column grid: short uppercase mono label, ink value.
 *
 *   <KV>
 *     <KV.Row label="Target"><a href="...">aws-prod-us-east-1</a></KV.Row>
 *     <KV.Row label="Region">us-east-1</KV.Row>
 *   </KV>
 */
interface KVProps {
  children: React.ReactNode
  className?: string
}

export function KV({ children, className }: KVProps) {
  return (
    <dl
      className={cn(
        'grid grid-cols-[auto_1fr] gap-x-3.5 gap-y-2 font-mono text-xs',
        className,
      )}
    >
      {children}
    </dl>
  )
}

interface KVRowProps {
  label: string
  children: React.ReactNode
}

function KVRow({ label, children }: KVRowProps) {
  return (
    <>
      <dt className="self-center text-[10px] uppercase tracking-[0.14em] text-ink-3">
        {label}
      </dt>
      <dd className="m-0 text-ink-2">{children}</dd>
    </>
  )
}

KV.Row = KVRow
