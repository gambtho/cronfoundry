// web/src/components/Layout.tsx
import { Outlet, NavLink, useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { cn } from '../lib/cn'

/**
 * Information architecture:
 *
 *   Operate                ← daily-use surfaces
 *     Overview             ← triage: what's failing, what's next
 *     Jobs                 ← primary index: every scheduled job
 *
 *   Configure
 *     Settings ▾           ← collapsed group; touched weekly at most
 *       Source repos
 *       Secrets
 *       Providers
 *       Users
 *       Audit log
 *
 * "Runs" is intentionally NOT top-level. Cross-job triage happens on
 * Overview; per-job runs live on Job detail. A filtered runs feed is
 * still reachable as a deep link from Overview's activity card.
 */

interface NavItem {
  to: string
  label: string
  /** Optional badge in the right margin (count, alert, etc.). */
  badge?: { text: string; tone?: 'default' | 'alert' | 'ok' }
}

export default function Layout() {
  // Live counts in the sidebar — schedules.list() drives both the
  // Jobs total and the Overview alert badge.
  const { data: schedules } = useQuery({
    queryKey: ['schedules'],
    queryFn: api.schedules.list,
    refetchInterval: 30_000,
  })
  const { data: recentRuns } = useQuery({
    queryKey: ['runs', { limit: 100 }],
    queryFn: () => api.runs.list({ limit: 100 }),
    refetchInterval: 15_000,
  })

  const failingCount = (() => {
    if (!schedules || !recentRuns) return 0
    // A schedule is "failing" if its most recent run failed.
    const lastBySchedule = new Map<string, (typeof recentRuns)[number]>()
    for (const r of recentRuns) {
      if (!lastBySchedule.has(r.schedule_name)) {
        lastBySchedule.set(r.schedule_name, r)
      }
    }
    return [...lastBySchedule.values()].filter(
      (r) => r.status === 'failed' || r.status === 'partial_failure',
    ).length
  })()

  const operateItems: NavItem[] = [
    {
      to: '/overview',
      label: 'Overview',
      // Operator-facing signal:
      //   failing → red count + ✕   (drag the eye to the problem)
      //   healthy → muted green ✓   (positive confirmation, not alarm)
      // We only show the green ✓ once data has loaded so we don't
      // flash a false "all good" during initial fetch.
      badge:
        failingCount > 0
          ? { text: `${failingCount} ✕`, tone: 'alert' }
          : schedules && recentRuns
            ? { text: '✓', tone: 'ok' }
            : undefined,
    },
    {
      to: '/jobs',
      label: 'Jobs',
      badge: schedules ? { text: String(schedules.length) } : undefined,
    },
  ]

  const settingsItems: NavItem[] = [
    { to: '/settings/repos', label: 'Source repos' },
    { to: '/settings/secrets', label: 'Secrets' },
    { to: '/settings/providers', label: 'Providers' },
    { to: '/settings/users', label: 'Users' },
    { to: '/settings/audit', label: 'Audit log' },
  ]

  const { data: me } = useQuery({ queryKey: ['me'], queryFn: api.me })

  return (
    <div className="grid min-h-screen grid-cols-[230px_1fr] bg-bg text-ink">
      <aside className="flex flex-col border-r border-rule bg-bg-2">
        {/* brand */}
        <div className="flex items-center gap-2.5 border-b border-rule px-4 py-4 font-mono text-[13px] tracking-[0.04em]">
          <span className="h-2 w-2 rounded-full bg-accent-green shadow-[0_0_10px_var(--green)]" />
          <span className="font-medium text-ink">
            cron<span className="text-accent-green">foundry</span>
          </span>
          <span className="ml-auto text-[10px] text-ink-3">v0.4</span>
        </div>

        {/* sections */}
        <NavSection title="Operate">
          {operateItems.map((item) => (
            <NavRow key={item.to} item={item} />
          ))}
        </NavSection>

        <NavSection title="Configure">
          <SettingsGroup items={settingsItems} />
        </NavSection>

        {/* footer */}
        <div className="mt-auto flex items-center justify-between border-t border-rule px-4 py-3 font-mono text-[11px] text-ink-3">
          <span className="flex items-center gap-2">
            <span className="flex h-[18px] w-[18px] items-center justify-center border border-rule-2 bg-bg-4 text-[9px] text-ink-2">
              {(me?.login ?? '..').slice(0, 2)}
            </span>
            <span>{me?.login ?? '…'}</span>
          </span>
          <a href="/oauth/logout" className="hover:text-ink">
            Sign out
          </a>
        </div>
      </aside>

      <main className="flex min-w-0 flex-col">
        <Outlet />
      </main>
    </div>
  )
}

/* -------------- internal nav primitives -------------- */

function NavSection({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}) {
  return (
    <div className="pb-1 pt-4">
      <h4 className="mx-4 mb-1.5 font-mono text-[10px] font-normal uppercase tracking-[0.16em] text-ink-3">
        {title}
      </h4>
      {children}
    </div>
  )
}

function NavRow({ item }: { item: NavItem }) {
  return (
    <NavLink
      to={item.to}
      className={({ isActive }) =>
        cn(
          'flex items-center gap-2.5 border-l-2 border-transparent px-4 py-1.5 text-[13px] text-ink-2',
          'hover:bg-bg-3 hover:text-ink',
          isActive && 'border-l-accent-green bg-bg-3 font-medium text-ink',
          isActive &&
            item.badge?.tone === 'alert' &&
            'border-l-accent-red',
        )
      }
    >
      <span>{item.label}</span>
      {item.badge && (
        <span
          className={cn(
            'ml-auto font-mono text-[11px]',
            item.badge.tone === 'alert' && 'font-medium text-accent-red',
            item.badge.tone === 'ok' && 'text-accent-green',
            item.badge.tone !== 'alert' &&
              item.badge.tone !== 'ok' &&
              'text-ink-3',
          )}
        >
          {item.badge.text}
        </span>
      )}
    </NavLink>
  )
}

function SettingsGroup({ items }: { items: NavItem[] }) {
  // Auto-open whenever the user is on a settings sub-route, so they
  // see where they are in the tree. Closed by default elsewhere
  // because settings are touched weekly at most.
  const { pathname } = useLocation()
  const isOnSettings = pathname.startsWith('/settings/')

  return (
    <details className="group mt-1" open={isOnSettings}>
      <summary
        className={cn(
          'flex cursor-pointer list-none items-center gap-2.5 px-4 py-1.5 text-[13px] text-ink-2',
          'hover:text-ink',
          // hide the default disclosure triangle
          '[&::-webkit-details-marker]:hidden',
        )}
      >
        <span>Settings</span>
        <span className="ml-auto text-sm text-ink-3 transition-transform group-open:rotate-90">
          ›
        </span>
      </summary>
      {items.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          className={({ isActive }) =>
            cn(
              'flex items-center border-l-2 border-transparent py-1.5 pl-[38px] pr-4 text-xs text-ink-2',
              'hover:bg-bg-3 hover:text-ink',
              isActive && 'border-l-accent-green bg-bg-3 text-ink',
            )
          }
        >
          {item.label}
        </NavLink>
      ))}
    </details>
  )
}
