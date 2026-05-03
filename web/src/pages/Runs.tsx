// web/src/pages/Runs.tsx
import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import {
  Card,
  DataTable,
  Input,
  PageHeader,
  Pill,
  Topbar,
  pillFromRunStatus,
} from '../components/ui'
import { formatDuration } from '../lib/derived'
import { relativeTime } from '../lib/time'
import type { RunStatus } from '../lib/types'

/**
 * Runs — chronological feed of all runs across all schedules.
 *
 * No top-level nav lives here; this page is a deep-link target from
 * Overview's activity card and from each Job. It still needs to be
 * fast to scan and filterable: status chip + free-text filter on
 * schedule/skill/repo/error.
 */

type StatusFilter = 'all' | 'failed' | 'running' | 'succeeded'

export default function Runs() {
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [query, setQuery] = useState('')

  // Optional ?schedule_id=… filter so deep-links from JobDetail
  // (and Overview's failure rows in the future) land on a pre-scoped
  // feed. The backend honours schedule_id; we just plumb it through.
  const [searchParams, setSearchParams] = useSearchParams()
  const scheduleId = searchParams.get('schedule_id') ?? undefined

  // Schedules list — needed only when a filter is active so we can
  // show "Filtered by <name> · clear" in the header. Cached by
  // Layout, so this is effectively free.
  const schedulesQ = useQuery({
    queryKey: ['schedules'],
    queryFn: api.schedules.list,
    enabled: !!scheduleId,
  })
  const filterSchedule = scheduleId
    ? schedulesQ.data?.find((s) => s.id === scheduleId)
    : undefined

  const runsQ = useQuery({
    queryKey: ['runs', { schedule_id: scheduleId, limit: 200 }],
    queryFn: () => api.runs.list({ schedule_id: scheduleId, limit: 200 }),
    refetchInterval: 10_000,
  })

  const runs = runsQ.data ?? []

  const counts = useMemo(() => {
    let failed = 0
    let running = 0
    let succeeded = 0
    for (const r of runs) {
      if (r.status === 'failed' || r.status === 'partial_failure') failed++
      else if (r.status === 'running') running++
      else if (r.status === 'succeeded') succeeded++
    }
    return { all: runs.length, failed, running, succeeded }
  }, [runs])

  const matchStatus = (s: RunStatus): boolean => {
    switch (statusFilter) {
      case 'failed':
        return s === 'failed' || s === 'partial_failure'
      case 'running':
        return s === 'running'
      case 'succeeded':
        return s === 'succeeded'
      default:
        return true
    }
  }

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    return runs.filter((r) => {
      if (!matchStatus(r.status)) return false
      if (q === '') return true
      return (
        r.schedule_name.toLowerCase().includes(q) ||
        r.skill_path?.toLowerCase().includes(q) ||
        r.repo_name?.toLowerCase().includes(q) ||
        r.owner?.toLowerCase().includes(q) ||
        r.error_kind?.toLowerCase().includes(q) ||
        r.error_msg?.toLowerCase().includes(q) ||
        r.fire_reason.toLowerCase().includes(q)
      )
    })
  }, [runs, query, statusFilter])

  const eyebrow =
    counts.failed > 0
      ? ({
          tone: 'fail',
          text: `${counts.failed} failed · ${counts.running} running · ${counts.succeeded} succeeded`,
        } as const)
      : counts.running > 0
        ? ({
            tone: 'warn',
            text: `${counts.running} running · ${counts.succeeded} succeeded`,
          } as const)
        : ({
            tone: 'ok',
            text: `${counts.all} runs · all clear`,
          } as const)

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Here>Runs</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[1320px] px-6 pb-16 pt-7">
        <PageHeader
          eyebrow={eyebrow}
          title="Runs"
          subtitle={
            filterSchedule
              ? `runs for ${filterSchedule.name}`
              : 'recent executions across every job'
          }
        />

        {scheduleId && (
          <div className="mb-3.5 flex items-center gap-2 rounded border border-rule bg-bg-2 px-3 py-2 font-mono text-[11.5px] text-ink-2">
            <span className="text-ink-3">filter:</span>
            <span className="text-ink">
              schedule = {filterSchedule?.name ?? scheduleId}
            </span>
            <button
              type="button"
              onClick={() => {
                const next = new URLSearchParams(searchParams)
                next.delete('schedule_id')
                setSearchParams(next, { replace: true })
              }}
              className="ml-auto text-ink-3 hover:text-ink"
              aria-label="Clear schedule filter"
            >
              clear ✕
            </button>
          </div>
        )}

        {/* toolbar */}
        <div className="mb-3.5 flex items-center gap-2 py-2.5 font-mono text-[11.5px]">
          <Chip
            on={statusFilter === 'all'}
            onClick={() => setStatusFilter('all')}
            count={counts.all}
          >
            All
          </Chip>
          <Chip
            on={statusFilter === 'failed'}
            tone="alert"
            onClick={() => setStatusFilter('failed')}
            count={counts.failed}
          >
            Failed
          </Chip>
          <Chip
            on={statusFilter === 'running'}
            onClick={() => setStatusFilter('running')}
            count={counts.running}
          >
            Running
          </Chip>
          <Chip
            on={statusFilter === 'succeeded'}
            onClick={() => setStatusFilter('succeeded')}
            count={counts.succeeded}
          >
            Succeeded
          </Chip>
          <span className="ml-auto">
            <Input
              placeholder="Filter by job, repo, error…"
              aria-label="Filter runs"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="w-64"
            />
          </span>
        </div>

        <Card>
          <DataTable>
            <DataTable.Head>
              <DataTable.HeadCell>Status</DataTable.HeadCell>
              <DataTable.HeadCell>Job</DataTable.HeadCell>
              <DataTable.HeadCell>Started</DataTable.HeadCell>
              <DataTable.HeadCell>Duration</DataTable.HeadCell>
              <DataTable.HeadCell>Trigger</DataTable.HeadCell>
              <DataTable.HeadCell>Run</DataTable.HeadCell>
            </DataTable.Head>
            <tbody>
              {runsQ.isError ? (
                <DataTable.Message colSpan={6} tone="error">
                  Could not load runs:{' '}
                  {runsQ.error instanceof Error
                    ? runsQ.error.message
                    : 'request failed'}
                  .{' '}
                  <button
                    type="button"
                    onClick={() => runsQ.refetch()}
                    className="text-ink underline-offset-2 hover:underline"
                  >
                    Retry
                  </button>
                </DataTable.Message>
              ) : runsQ.isLoading ? (
                <DataTable.Message colSpan={6}>Loading runs…</DataTable.Message>
              ) : visible.length === 0 ? (
                <DataTable.Message colSpan={6}>
                  {runs.length === 0
                    ? 'No runs yet. Wait for a schedule to fire, or hit Run now on a job.'
                    : 'No runs match this filter.'}
                </DataTable.Message>
              ) : (
                visible.map((r) => {
                  const { variant, label } = pillFromRunStatus(r.status)
                  const isFailing =
                    r.status === 'failed' || r.status === 'partial_failure'
                  return (
                    <DataTable.Row key={r.id} alert={isFailing}>
                      <DataTable.Cell>
                        <Pill variant={variant}>
                          {r.error_kind ?? label}
                        </Pill>
                      </DataTable.Cell>
                      <DataTable.Cell className="text-ink">
                        {r.schedule_name}
                        <span className="mt-0.5 block text-[11px] text-ink-3">
                          {r.owner}/{r.repo_name}
                        </span>
                      </DataTable.Cell>
                      <DataTable.Cell>
                        {r.started_at
                          ? new Date(r.started_at).toLocaleString(undefined, {
                              dateStyle: 'medium',
                              timeStyle: 'short',
                            })
                          : '—'}
                        <span className="mt-0.5 block text-[11px] text-ink-3">
                          {r.created_at ? relativeTime(r.created_at) : ''}
                        </span>
                      </DataTable.Cell>
                      <DataTable.Cell>
                        {r.duration_ms != null
                          ? formatDuration(r.duration_ms)
                          : '—'}
                      </DataTable.Cell>
                      <DataTable.Cell>
                        {r.fire_reason}
                        {r.actor && (
                          <span className="mt-0.5 block text-[11px] text-ink-3">
                            by {r.actor}
                          </span>
                        )}
                      </DataTable.Cell>
                      <DataTable.Cell>
                        <Link
                          to={`/runs/${r.id}`}
                          className="text-ink-3 hover:text-accent-cyan"
                        >
                          #{r.id.slice(0, 8)}
                        </Link>
                      </DataTable.Cell>
                    </DataTable.Row>
                  )
                })
              )}
            </tbody>
          </DataTable>
        </Card>

        <p className="mt-8 flex justify-between border-t border-rule pt-3 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
          <span>
            showing {visible.length} of {counts.all} runs
          </span>
          <span>{runsQ.isFetching ? 'refreshing…' : 'live'}</span>
        </p>
      </div>
    </>
  )
}

interface ChipProps {
  on: boolean
  count: number
  tone?: 'default' | 'alert'
  onClick: () => void
  children: React.ReactNode
}
function Chip({
  on,
  count,
  tone = 'default',
  onClick,
  children,
}: ChipProps) {
  const isAlert = tone === 'alert'
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={on}
      className={
        'inline-flex items-center gap-1.5 rounded border px-2.5 py-1 transition-colors ' +
        (isAlert
          ? on
            ? 'border-accent-red-dim bg-red-bg text-accent-red'
            : 'border-rule-2 bg-bg-2 text-accent-red hover:border-ink-3'
          : on
            ? 'border-ink-3 bg-bg-3 text-ink'
            : 'border-rule-2 bg-bg-2 text-ink-2 hover:border-ink-3 hover:text-ink')
      }
    >
      {children}
      <span className={isAlert && on ? 'text-accent-red' : 'text-ink-3'}>
        {count}
      </span>
    </button>
  )
}
