import { cn } from '../../lib/cn'

/**
 * PageHeader — the editorial slab at the top of every page.
 *
 *   <PageHeader
 *     eyebrow={{ tone: 'fail', text: '2 jobs failing' }}
 *     title="nightly-backup"
 *     subtitle="postgres → s3"
 *     actions={<Button>...</Button>}
 *   >
 *     <PageHeader.Meta>...optional chips/text...</PageHeader.Meta>
 *   </PageHeader>
 *
 * The eyebrow's tone drives the dot color; absence of an eyebrow is
 * the implicit "all good" state — we don't add a green eyebrow for
 * the healthy case, because absence of red is the win state.
 */
type EyebrowTone = 'ok' | 'fail' | 'warn'

interface Eyebrow {
  tone: EyebrowTone
  text: string
}

interface PageHeaderProps {
  eyebrow?: Eyebrow
  title: React.ReactNode
  subtitle?: React.ReactNode
  actions?: React.ReactNode
  children?: React.ReactNode
}

const EYEBROW_TONE: Record<EyebrowTone, { text: string; dot: string }> = {
  ok: {
    text: 'text-accent-green',
    dot: 'bg-accent-green shadow-[0_0_8px_var(--green)]',
  },
  fail: {
    text: 'text-accent-red',
    dot: 'bg-accent-red shadow-[0_0_8px_var(--red)]',
  },
  warn: {
    text: 'text-accent-amber',
    dot: 'bg-accent-amber shadow-[0_0_8px_rgba(232,179,57,0.6)]',
  },
}

export function PageHeader({
  eyebrow,
  title,
  subtitle,
  actions,
  children,
}: PageHeaderProps) {
  return (
    <header className="mb-5 grid grid-cols-[1fr_auto] items-start gap-8 border-b border-rule pb-5">
      <div>
        {eyebrow && (
          <div
            className={cn(
              'mb-2.5 flex items-center gap-2.5 font-mono text-[11px] uppercase tracking-[0.14em]',
              EYEBROW_TONE[eyebrow.tone].text,
            )}
          >
            <span
              className={cn(
                'h-1.5 w-1.5 rounded-full animate-pulse',
                EYEBROW_TONE[eyebrow.tone].dot,
              )}
            />
            {eyebrow.text}
          </div>
        )}
        <h1 className="m-0 text-[28px] font-semibold leading-[1.15] tracking-[-0.02em] text-ink">
          {title}
          {subtitle && (
            <span className="mt-1.5 block font-mono text-[13px] font-normal tracking-normal text-ink-2">
              {subtitle}
            </span>
          )}
        </h1>
        {children}
      </div>
      {actions && (
        <div className="flex min-w-[200px] flex-col items-stretch gap-1.5">
          {actions}
        </div>
      )}
    </header>
  )
}

function PageHeaderMeta({ children }: { children: React.ReactNode }) {
  return (
    <div className="mt-3.5 flex flex-wrap items-center gap-4 font-mono text-[11.5px] text-ink-3">
      {children}
    </div>
  )
}

function PageHeaderChip({ children }: { children: React.ReactNode }) {
  return (
    <span className="rounded border border-rule-2 px-2 py-0.5 text-ink-2">
      {children}
    </span>
  )
}

PageHeader.Meta = PageHeaderMeta
PageHeader.Chip = PageHeaderChip
