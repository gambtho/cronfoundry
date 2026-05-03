// web/src/pages/Jobs.tsx
import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { useShortcut } from '../lib/useShortcut'
import {
  Button,
  Card,
  IconButton,
  PageHeader,
  Pill,
  Topbar,
  pillFromRunStatus,
} from '../components/ui'
import {
  failingSchedules,
  formatCountdown,
  formatDuration,
  lastRunByScheduleName,
} from '../lib/derived'
import { relativeTime } from '../lib/time'
import { cn } from '../lib/cn'
import type { RunSummary, Schedule } from '../lib/types'

/**
 * Jobs — the primary index. Replaces the table-y dashboard.
 *
 * IA decisions baked into this page:
 *   - "Job" is just a Schedule. A Job's name, schedule, last status,
 *     next run, provider, and 14-day history all live here.
 *   - Failing rows pin to the top via `sortMode === 'alert'`.
 *   - Filter chips drive client-side filtering only — the API doesn't
 *     filter by status, so we list everything and slice locally.
 *   - Click affordance is the job-name anchor (no row-level click).
 */

type StatusFilter = 'all' | 'failing' | 'healthy' | 'paused'
type SortMode = 'alert' | 'name' | 'next'

export default function Jobs() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  useShortcut('n', () => navigate('/jobs/new'))
  const schedulesQ = useQuery({
    queryKey: ['schedules'],
    queryFn: api.schedules.list,
    refetchInterval: 30_000,
  })
  const runsQ = useQuery({
    queryKey: ['runs', { limit: 200 }],
    queryFn: () => api.runs.list({ limit: 200 }),
    refetchInterval: 15_000,
  })

  const runNow = useMutation({
    mutationFn: api.schedules.runNow,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['runs'] }),
  })
  const resume = useMutation({
    mutationFn: api.schedules.resume,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  const [filter, setFilter] = useState<StatusFilter>('all')
  const [sortMode, setSortMode] = useState<SortMode>('alert')
  const [query, setQuery] = useState('')

  const schedules = schedulesQ.data ?? []
  const runs = runsQ.data ?? []

  // ---- counts for the filter chips ----
  // Disjoint buckets — paused dominates, then failing, then healthy.
  // A paused-and-failing job belongs to Paused, not Failing, so the
  // chip totals add up to `all` exactly. The same membership rules
  // are used by the row filter below.
  const lastByName = useMemo(() => lastRunByScheduleName(runs), [runs])
  const failingIds = useMemo(
    () => new Set(failingSchedules(schedules, runs).map((f) => f.schedule.id)),
    [schedules, runs],
  )
  const bucketOf = (s: Schedule): 'paused' | 'failing' | 'healthy' => {
    if (!s.enabled) return 'paused'
    if (failingIds.has(s.id)) return 'failing'
    return 'healthy'
  }
  const counts = useMemo(() => {
    let paused = 0
    let failing = 0
    let healthy = 0
    for (const s of schedules) {
      const b = bucketOf(s)
      if (b === 'paused') paused++
      else if (b === 'failing') failing++
      else healthy++
    }
    return { all: schedules.length, failing, paused, healthy }
    // bucketOf depends on failingIds; lint trusts the inputs covered.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [schedules, failingIds])

  // ---- filtered + sorted list ----
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    const matchQuery = (s: Schedule) =>
      q === '' ||
      s.name.toLowerCase().includes(q) ||
      s.skill_name?.toLowerCase().includes(q) ||
      s.repo_name?.toLowerCase().includes(q) ||
      s.cron.includes(q)

    let list = schedules.filter(matchQuery)
    list = list.filter((s) => {
      // Use the same paused-first bucketing as the chip counts so the
      // numbers and the table always agree.
      switch (filter) {
        case 'failing':
          return bucketOf(s) === 'failing'
        case 'healthy':
          return bucketOf(s) === 'healthy'
        case 'paused':
          return bucketOf(s) === 'paused'
        default:
          return true
      }
    })

    list = [...list]
    list.sort((a, b) => {
      if (sortMode === 'name') return a.name.localeCompare(b.name)
      if (sortMode === 'next') {
        const ax = a.next_fire_at ? new Date(a.next_fire_at).getTime() : Infinity
        const bx = b.next_fire_at ? new Date(b.next_fire_at).getTime() : Infinity
        return ax - bx
      }
      // alert-first: failing > healthy > paused
      const score = (s: Schedule) =>
        failingIds.has(s.id) ? 0 : !s.enabled ? 2 : 1
      const diff = score(a) - score(b)
      return diff !== 0 ? diff : a.name.localeCompare(b.name)
    })
    return list
  }, [schedules, query, filter, failingIds, sortMode])

  const eyebrow =
    counts.failing > 0
      ? ({
          tone: 'fail',
          text: `${counts.failing} failing · ${counts.healthy} healthy · ${counts.paused} paused`,
        } as const)
      : ({
          tone: 'ok',
          text: `${counts.all} jobs · ${counts.healthy} healthy${
            counts.paused ? ` · ${counts.paused} paused` : ''
          }`,
        } as const)

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Here>Jobs</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[1320px] px-6 pb-16 pt-7">
        <PageHeader
          eyebrow={eyebrow}
          title="Jobs"
          subtitle="scheduled tasks across your repos and providers"
          actions={
            <>
              <Button variant="primary" shortcut="N" onClick={() => navigate('/jobs/new')}>+ Add job</Button>
              <Button onClick={() => navigate('/jobs/import')}>+ Import job</Button>
            </>
          }
        />

        {/* ---------- toolbar ---------- */}
        <div className="mb-3.5 flex items-center gap-2 py-2.5 font-mono text-[11.5px]">
          <FilterChip
            on={filter === 'all'}
            onClick={() => setFilter('all')}
            count={counts.all}
          >
            All
          </FilterChip>
          <FilterChip
            on={filter === 'failing'}
            tone="alert"
            onClick={() => setFilter('failing')}
            count={counts.failing}
          >
            Failing
          </FilterChip>
          <FilterChip
            on={filter === 'healthy'}
            onClick={() => setFilter('healthy')}
            count={counts.healthy}
          >
            Healthy
          </FilterChip>
          <FilterChip
            on={filter === 'paused'}
            onClick={() => setFilter('paused')}
            count={counts.paused}
          >
            Paused
          </FilterChip>
          <span className="mx-1.5 text-ink-3">·</span>

          <input
            className="rounded border border-rule-2 bg-bg-2 px-2.5 py-1 text-ink placeholder:text-ink-3 focus:border-ink-3 focus:outline-none"
            placeholder="Filter by name, repo, cron…"
            aria-label="Filter jobs"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />

          <span className="ml-auto flex items-center gap-2">
            <label className="flex items-center gap-1.5 text-ink-3">
              <span>sort</span>
              <select
                value={sortMode}
                onChange={(e) => setSortMode(e.target.value as SortMode)}
                className="rounded border border-rule-2 bg-bg-2 px-1.5 py-0.5 text-ink"
              >
                <option value="alert">alert first</option>
                <option value="name">name</option>
                <option value="next">next run</option>
              </select>
            </label>
          </span>
        </div>

        {/* ---------- table ---------- */}
        <Card>
          {schedulesQ.isError ? (
            <div className="px-4 py-12 text-center font-mono text-[12px] text-accent-red">
              Could not load jobs:{' '}
              {schedulesQ.error instanceof Error
                ? schedulesQ.error.message
                : 'request failed'}
              .{' '}
              <button
                type="button"
                onClick={() => schedulesQ.refetch()}
                className="text-ink underline-offset-2 hover:underline"
              >
                Retry
              </button>
            </div>
          ) : schedulesQ.isLoading ? (
            <div className="px-4 py-12 text-center font-mono text-[12px] text-ink-3">
              Loading jobs…
            </div>
          ) : visible.length === 0 ? (
            <div className="px-4 py-12 text-center font-mono text-[12px] text-ink-3">
              {schedules.length === 0
                ? 'No jobs yet. Connect a repo with cron schedules to start.'
                : 'No jobs match this filter.'}
            </div>
          ) : (
            <table className="w-full border-collapse bg-bg-2 font-mono text-[12px]">
              <thead>
                <tr className="border-y border-rule bg-bg-3 text-[10px] uppercase tracking-[0.16em] text-ink-3">
                  <th className="whitespace-nowrap px-4 py-2.5 text-left font-medium">
                    Job
                  </th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-left font-medium">
                    Schedule
                  </th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-left font-medium">
                    Last run
                  </th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-left font-medium">
                    Next run
                  </th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-left font-medium">
                    Provider
                  </th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">
                    Actions
                  </th>
                </tr>
              </thead>
              <tbody>
                {visible.map((s) => (
                  <JobRow
                    key={s.id}
                    schedule={s}
                    lastRun={lastByName.get(s.name)}
                    isFailing={failingIds.has(s.id)}
                    onRunNow={() => runNow.mutate(s.id)}
                    onResume={() => resume.mutate(s.id)}
                    runDisabled={runNow.isPending}
                    resumeDisabled={resume.isPending}
                  />
                ))}
              </tbody>
            </table>
          )}
        </Card>

        <p className="mt-8 flex justify-between border-t border-rule pt-3 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
          <span>
            showing {visible.length} of {counts.all} jobs · sort {sortMode}
          </span>
          <span>{schedulesQ.isLoading ? 'loading…' : 'live'}</span>
        </p>
      </div>
    </>
  )
}

/* --------------------- subcomponents --------------------- */

interface JobRowProps {
  schedule: Schedule
  lastRun?: RunSummary
  isFailing: boolean
  onRunNow: () => void
  onResume: () => void
  runDisabled: boolean
  resumeDisabled: boolean
}

function JobRow({
  schedule: s,
  lastRun,
  isFailing,
  onRunNow,
  onResume,
  runDisabled,
  resumeDisabled,
}: JobRowProps) {
  const paused = !s.enabled
  return (
    <tr
      className={cn(
        'border-b border-rule transition-colors hover:bg-bg-3',
        isFailing && 'shadow-[inset_2px_0_0_var(--red)]',
        paused && 'opacity-60',
      )}
    >
      <td className="px-4 py-3 align-middle">
        <Link
          to={`/jobs/${s.id}`}
          className="text-[13px] font-medium text-ink hover:text-accent-green"
        >
          {s.name}
        </Link>
        <span className="mt-0.5 block text-[11px] text-ink-3">
          {paused
            ? `⏸ paused${s.auto_paused_at ? ` ${relativeTime(s.auto_paused_at)}` : ''}`
            : `${s.skill_name ?? s.skill_path}`}
        </span>
      </td>
      <td className="px-4 py-3 align-middle">
        <span className="text-accent-cyan">{s.cron}</span>
        <span className="mt-0.5 block text-[11px] text-ink-3">
          {s.timezone}
        </span>
      </td>
      <td className="px-4 py-3 align-middle">
        {lastRun ? (
          <>
            {(() => {
              const { variant, label } = pillFromRunStatus(lastRun.status)
              return (
                <Pill variant={variant}>
                  {lastRun.error_kind ?? label}
                </Pill>
              )
            })()}
            <span className="mt-0.5 block text-[11px] text-ink-3">
              {lastRun.created_at ? relativeTime(lastRun.created_at) : '—'}
              {lastRun.duration_ms != null
                ? ` · ${formatDuration(lastRun.duration_ms)}`
                : ''}
            </span>
          </>
        ) : (
          <span className="text-ink-3">never run</span>
        )}
      </td>
      <td className="whitespace-nowrap px-4 py-3 align-middle">
        {s.next_fire_at
          ? `in ${formatCountdown(new Date(s.next_fire_at).getTime() - Date.now())}`
          : paused
            ? '—'
            : '?'}
      </td>
      <td className="px-4 py-3 align-middle">{s.provider || '—'}</td>
      <td className="px-4 py-3 text-right align-middle">
        {paused ? (
          <IconButton
            aria-label={`Resume ${s.name}`}
            onClick={onResume}
            disabled={resumeDisabled}
          >
            ▶
          </IconButton>
        ) : (
          <IconButton
            aria-label={`Run ${s.name} now`}
            onClick={onRunNow}
            disabled={runDisabled}
          >
            ▶
          </IconButton>
        )}
      </td>
    </tr>
  )
}

interface FilterChipProps {
  on: boolean
  count?: number
  tone?: 'default' | 'alert'
  onClick: () => void
  children: React.ReactNode
}
function FilterChip({ on, count, tone = 'default', onClick, children }: FilterChipProps) {
  const isAlert = tone === 'alert'
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={on}
      className={cn(
        'inline-flex items-center gap-1.5 rounded border px-2.5 py-1 transition-colors',
        'border-rule-2 bg-bg-2 text-ink-2 hover:border-ink-3 hover:text-ink',
        on && 'bg-bg-3 text-ink',
        on && !isAlert && 'border-ink-3',
        isAlert && 'border-accent-red-dim text-accent-red',
        on && isAlert && 'bg-red-bg',
      )}
    >
      {children}
      {count != null && (
        <span className={cn('text-ink-3', isAlert && on && 'text-accent-red')}>
          {count}
        </span>
      )}
    </button>
  )
}
