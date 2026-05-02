import { cn } from '../../lib/cn'

/**
 * Topbar — slim header strip above the page content.
 * Holds breadcrumbs on the left, search + ⌘K hint on the right.
 *
 *   <Topbar>
 *     <Topbar.Crumbs>
 *       <Topbar.Crumb href="/jobs">Jobs</Topbar.Crumb>
 *       <Topbar.Sep />
 *       <Topbar.Here>nightly-backup</Topbar.Here>
 *     </Topbar.Crumbs>
 *     <Topbar.Search />
 *   </Topbar>
 */
export function Topbar({ children }: { children: React.ReactNode }) {
  return (
    <div
      className={cn(
        'flex h-[46px] items-center gap-3.5 border-b border-rule bg-bg-2 px-6',
        'font-mono text-xs text-ink-2',
      )}
    >
      {children}
    </div>
  )
}

function Crumbs({ children }: { children: React.ReactNode }) {
  return <nav className="flex items-center gap-2">{children}</nav>
}

function Crumb({
  href,
  children,
}: {
  href: string
  children: React.ReactNode
}) {
  return (
    <a href={href} className="text-ink-2 hover:text-ink">
      {children}
    </a>
  )
}

function Sep() {
  return <span className="text-ink-4">/</span>
}

function Here({ children }: { children: React.ReactNode }) {
  return <span className="text-ink">{children}</span>
}

function Spacer() {
  return <div className="flex-1" />
}

function Search({
  placeholder = 'Jump to job, run, secret…',
  label = 'Search jobs, runs, and secrets',
}: {
  placeholder?: string
  /** Accessible name announced by screen readers. */
  label?: string
}) {
  return (
    <>
      <input
        type="search"
        aria-label={label}
        className={cn(
          'w-[280px] rounded border border-rule bg-bg px-2.5 py-1.5',
          'font-mono text-xs text-ink placeholder:text-ink-3',
          'focus:border-ink-3 focus:outline-none',
        )}
        placeholder={placeholder}
      />
      <span
        aria-hidden="true"
        className="rounded border border-rule bg-bg px-1.5 text-[10px] text-ink-2"
      >
        ⌘K
      </span>
    </>
  )
}

Topbar.Crumbs = Crumbs
Topbar.Crumb = Crumb
Topbar.Sep = Sep
Topbar.Here = Here
Topbar.Spacer = Spacer
Topbar.Search = Search
