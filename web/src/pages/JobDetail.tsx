// web/src/pages/JobDetail.tsx
import { useState } from 'react'
import { Link, useParams, Navigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import {
  Button,
  Card,
  KV,
  PageHeader,
  Pill,
  Topbar,
  pillFromRunStatus,
} from '../components/ui'
import { ScheduleOverrideForm } from '../components/ScheduleOverrideForm'
import { formatCountdown, formatDuration } from '../lib/derived'
import { relativeTime } from '../lib/time'
import { cn } from '../lib/cn'
import type { RunSummary, Schedule } from '../lib/types'

/**
 * JobDetail — the debugging hub. One page, every question:
 *
 *   "What broke?"        → failure banner at top with last error
 *   "When does it run?"  → cron strip with next-fire countdown
 *   "What's the trend?"  → recent runs sparkline + table
 *   "Who does it call?"  → right rail (provider, secrets via env, source)
 *   "What did it say?"   → tabs (logs / definition / env / history)
 *
 * "Job" is the existing Schedule entity. We resolve by `:id` against
 * schedules.list() because the API doesn't yet expose a single-schedule
 * GET endpoint. (One round trip already lives in Layout's badge,
 * react-query dedupes, so this is effectively free.)
 */
export default function JobDetail() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()

  const schedulesQ = useQuery({
    queryKey: ['schedules'],
    queryFn: api.schedules.list,
    refetchInterval: 30_000,
  })

  const runsQ = useQuery({
    queryKey: ['runs', { schedule_id: id, limit: 20 }],
    queryFn: () => api.runs.list({ schedule_id: id, limit: 20 }),
    refetchInterval: 15_000,
    enabled: !!id,
  })

  const runNow = useMutation({
    mutationFn: api.schedules.runNow,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['runs'] }),
  })
  const pause = useMutation({
    mutationFn: api.schedules.pause,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })
  const resume = useMutation({
    mutationFn: api.schedules.resume,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  const [editing, setEditing] = useState(false)

  if (!id) return <Navigate to="/jobs" replace />

  const schedule = schedulesQ.data?.find((s) => s.id === id)
  const runs = runsQ.data ?? []

  if (schedulesQ.isError && !schedule) {
    return (
      <>
        <Topbar>
          <Topbar.Crumbs>
            <Topbar.Crumb href="/jobs">Jobs</Topbar.Crumb>
            <Topbar.Sep />
            <Topbar.Here>error</Topbar.Here>
          </Topbar.Crumbs>
          <Topbar.Spacer />
          <Topbar.Search />
        </Topbar>
        <div className="px-6 py-12 text-center font-mono text-[12px] text-accent-red">
          Could not load job:{' '}
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
      </>
    )
  }

  if (schedulesQ.isSuccess && !schedule) {
    return (
      <>
        <Topbar>
          <Topbar.Crumbs>
            <Topbar.Crumb href="/jobs">Jobs</Topbar.Crumb>
            <Topbar.Sep />
            <Topbar.Here>not found</Topbar.Here>
          </Topbar.Crumbs>
          <Topbar.Spacer />
          <Topbar.Search />
        </Topbar>
        <div className="px-6 py-12 text-center font-mono text-[12px] text-ink-3">
          Job not found.{' '}
          <Link to="/jobs" className="text-ink hover:text-accent-green">
            Back to jobs →
          </Link>
        </div>
      </>
    )
  }

  if (!schedule) {
    return (
      <>
        <Topbar>
          <Topbar.Crumbs>
            <Topbar.Crumb href="/jobs">Jobs</Topbar.Crumb>
            <Topbar.Sep />
            <Topbar.Here>…</Topbar.Here>
          </Topbar.Crumbs>
          <Topbar.Spacer />
          <Topbar.Search />
        </Topbar>
        <div className="px-6 py-12 text-center font-mono text-[12px] text-ink-3">
          Loading…
        </div>
      </>
    )
  }

  const lastRun: RunSummary | undefined = runs[0]
  const previousFailure = runs.slice(1).find(
    (r) => r.status === 'failed' || r.status === 'partial_failure',
  )

  const isFailing =
    lastRun?.status === 'failed' || lastRun?.status === 'partial_failure'

  const failingStreak = (() => {
    let n = 0
    for (const r of runs) {
      if (r.status === 'failed' || r.status === 'partial_failure') n++
      else break
    }
    return n
  })()

  const successCount = runs.filter((r) => r.status === 'succeeded').length
  const successRate = runs.length === 0 ? null : Math.round((successCount / runs.length) * 100)

  const eyebrow = isFailing
    ? ({
        tone: 'fail',
        text:
          failingStreak > 1
            ? `Failing — last ${failingStreak} runs`
            : 'Failing — last run',
      } as const)
    : ({ tone: 'ok', text: 'Healthy' } as const)

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Crumb href="/jobs">Jobs</Topbar.Crumb>
          <Topbar.Sep />
          <Topbar.Here>{schedule.name}</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[1320px] px-6 pb-16 pt-7">
        <PageHeader
          eyebrow={eyebrow}
          title={schedule.name}
          subtitle={schedule.skill_name ?? schedule.skill_path}
          actions={
            <>
              <Button
                variant="primary"
                shortcut="R"
                onClick={() => runNow.mutate(schedule.id)}
                disabled={runNow.isPending}
              >
                ▶ Run now
              </Button>
              <Button shortcut="E" onClick={() => setEditing(true)}>
                Edit schedule
              </Button>
              {schedule.enabled ? (
                <Button
                  variant="ghost"
                  onClick={() => pause.mutate(schedule.id)}
                  disabled={pause.isPending}
                >
                  ⏸ Pause job
                </Button>
              ) : (
                <Button
                  variant="ghost"
                  onClick={() => resume.mutate(schedule.id)}
                  disabled={resume.isPending}
                >
                  ▶ Resume
                </Button>
              )}
            </>
          }
        >
          <PageHeader.Meta>
            <PageHeader.Chip>
              <b>{schedule.owner}/{schedule.repo_name}</b>
            </PageHeader.Chip>
            <span>{schedule.skill_path}</span>
            {schedule.has_ui_overrides && (
              <Pill variant="amber">Overrides active</Pill>
            )}
          </PageHeader.Meta>
        </PageHeader>

        {/* failure banner */}
        {isFailing && lastRun && (
          <div className="relative mb-4 grid grid-cols-[auto_1fr_auto] items-center gap-4 overflow-hidden rounded border border-accent-red-dim bg-red-bg px-4 py-3.5">
            <span className="absolute inset-y-0 left-0 w-[3px] bg-accent-red" />
            <div className="flex h-8 w-8 items-center justify-center rounded border border-accent-red font-mono text-[18px] font-semibold text-accent-red">
              !
            </div>
            <div>
              <strong className="block text-[14px] font-semibold text-ink">
                Run exited with {lastRun.error_kind ?? lastRun.status}
              </strong>
              <p className="m-0 mt-1 font-mono text-[12px] text-ink-2">
                {lastRun.created_at && relativeTime(lastRun.created_at)} on{' '}
                <code className="rounded-sm bg-[rgba(255,93,82,0.12)] px-1.5 py-0.5 text-[#ffb5af]">
                  {schedule.provider}
                </code>
                {lastRun.error_msg ? ` — ${lastRun.error_msg}` : ''}
                {previousFailure && (
                  <> · same failure as <Link to={`/runs/${previousFailure.id}`} className="text-accent-red underline-offset-2 hover:underline">previous run</Link></>
                )}
              </p>
            </div>
            <Link to={`/runs/${lastRun.id}`}>
              <Button>View logs →</Button>
            </Link>
          </div>
        )}

        {/* schedule strip */}
        <div className="mb-4 grid grid-cols-[auto_1fr_auto] items-center gap-6 rounded border border-rule bg-bg-2 px-5 py-3.5">
          <div className="font-mono text-[16px] tracking-[0.04em] text-accent-cyan">
            {schedule.cron}
          </div>
          <div className="text-[14px] text-ink-2">
            {humanizeCron(schedule.cron)} · {schedule.timezone}
          </div>
          <div className="text-right font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
            Next run
            <b className="mt-0.5 block text-[15px] font-medium normal-case tracking-normal text-ink">
              {schedule.next_fire_at
                ? `in ${formatCountdown(new Date(schedule.next_fire_at).getTime() - Date.now())}`
                : '—'}
            </b>
          </div>
        </div>

        {/* two columns */}
        <div className="grid grid-cols-[1fr_320px] gap-5">
          <div className="min-w-0">
            {/* Recent runs */}
            <Card>
              <Card.Header>
                <span>Recent runs</span>
                <Card.HeaderMeta>
                  {runs.length === 0
                    ? 'no runs yet'
                    : `last ${runs.length} runs`}
                </Card.HeaderMeta>
                <Link
                  to={`/runs?schedule_id=${schedule.id}`}
                  className="font-mono text-[11px] tracking-[0.04em] text-ink-2 hover:text-accent-green"
                >
                  All runs →
                </Link>
              </Card.Header>

              {runsQ.isError ? (
                <Card.Body>
                  <p className="m-0 font-mono text-[12px] text-accent-red">
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
                  </p>
                </Card.Body>
              ) : runsQ.isLoading ? (
                <Card.Body>
                  <p className="m-0 font-mono text-[12px] text-ink-3">
                    Loading runs…
                  </p>
                </Card.Body>
              ) : runs.length === 0 ? (
                <Card.Body>
                  <p className="m-0 font-mono text-[12px] text-ink-3">
                    No runs yet. Hit ▶ Run now or wait for the schedule.
                  </p>
                </Card.Body>
              ) : (
                <>
                  <div className="flex items-baseline justify-between px-4 pb-2 pt-3.5">
                    <div className="text-[16px] font-medium text-ink">
                      {successRate}% success
                      {failingStreak > 0 && (
                        <span className="text-accent-red">
                          {' · '}
                          {failingStreak} failing
                        </span>
                      )}
                    </div>
                    <div className="font-mono text-[11px] uppercase tracking-[0.12em] text-ink-3">
                      {medianDuration(runs)}
                    </div>
                  </div>

                  <Sparkline runs={runs} />

                  <table className="w-full border-collapse font-mono text-[12px]">
                    <thead>
                      <tr className="border-y border-rule bg-bg-3 text-[10px] uppercase tracking-[0.16em] text-ink-3">
                        <th className="px-4 py-2 text-left font-medium">Run</th>
                        <th className="px-4 py-2 text-left font-medium">
                          Status
                        </th>
                        <th className="px-4 py-2 text-left font-medium">
                          Started
                        </th>
                        <th className="px-4 py-2 text-left font-medium">
                          Duration
                        </th>
                        <th className="px-4 py-2 text-right font-medium">
                          Trigger
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {runs.map((r) => (
                        <RunRow key={r.id} run={r} />
                      ))}
                    </tbody>
                  </table>
                </>
              )}
            </Card>

            {/* Definition / logs / env tabs — currently a thin
                preview; full forensics live on Run detail. */}
            {lastRun && (
              <Card>
                <Card.Header>
                  <span>Last run · #{lastRun.id.slice(0, 8)}</span>
                  <Card.HeaderMeta>
                    {lastRun.duration_ms != null
                      ? formatDuration(lastRun.duration_ms)
                      : '—'}
                  </Card.HeaderMeta>
                  <Link
                    to={`/runs/${lastRun.id}`}
                    className="font-mono text-[11px] text-ink-2 hover:text-accent-green"
                  >
                    Open run →
                  </Link>
                </Card.Header>
                <Card.Body>
                  {lastRun.error_msg ? (
                    <pre className="m-0 overflow-x-auto rounded bg-[#06080a] p-3.5 font-mono text-[11.5px] leading-[1.7] text-ink">
                      <span className="text-accent-red">{lastRun.error_msg}</span>
                    </pre>
                  ) : (
                    <p className="m-0 font-mono text-[12px] text-ink-3">
                      No error message recorded for the most recent run.
                    </p>
                  )}
                </Card.Body>
              </Card>
            )}
          </div>

          {/* RIGHT RAIL */}
          <aside className="min-w-0">
            <Card>
              <Card.Header>Provider</Card.Header>
              <Card.Body>
                <KV>
                  <KV.Row label="Target">{schedule.provider}</KV.Row>
                  <KV.Row label="Model">{schedule.model}</KV.Row>
                  <KV.Row label="Timeout">{schedule.timeout_sec}s</KV.Row>
                  <KV.Row label="Overlap">{schedule.overlap_policy}</KV.Row>
                </KV>
              </Card.Body>
            </Card>

            {schedule.mcp_servers && schedule.mcp_servers.length > 0 && (
              <Card>
                <Card.Header>
                  <span>MCP servers</span>
                  <Card.HeaderMeta>{schedule.mcp_servers.length}</Card.HeaderMeta>
                </Card.Header>
                <ul className="m-0 list-none p-0 font-mono text-[12px]">
                  {schedule.mcp_servers.map((srv) => (
                    <li
                      key={srv.name}
                      className="flex items-center gap-2 border-b border-rule px-4 py-2 last:border-b-0"
                    >
                      <span className="text-amber-300">⚿</span>
                      <span className="text-ink">{srv.name}</span>
                      <span
                        className="ml-auto truncate text-ink-3"
                        title={`${srv.command} ${srv.args.join(' ')}`}
                      >
                        {srv.command}
                      </span>
                    </li>
                  ))}
                </ul>
              </Card>
            )}

            <Card>
              <Card.Header>Source</Card.Header>
              <Card.Body>
                <KV>
                  <KV.Row label="Repo">
                    <a
                      href={`https://github.com/${schedule.owner}/${schedule.repo_name}`}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      {schedule.owner}/{schedule.repo_name}
                    </a>
                  </KV.Row>
                  <KV.Row label="File">{schedule.skill_path}</KV.Row>
                  <KV.Row label="Last enabled">
                    {relativeTime(schedule.last_enabled_at)}
                  </KV.Row>
                </KV>
              </Card.Body>
            </Card>

            {!schedule.enabled && schedule.auto_paused_at && (
              <Card className="border-accent-red-dim">
                <Card.Header className="border-b-accent-red-dim bg-red-bg">
                  <span className="text-accent-red">Auto-paused</span>
                </Card.Header>
                <Card.Body>
                  <KV>
                    <KV.Row label="When">
                      {relativeTime(schedule.auto_paused_at)}
                    </KV.Row>
                    <KV.Row label="Reason">
                      {schedule.auto_pause_reason ?? '—'}
                    </KV.Row>
                  </KV>
                </Card.Body>
              </Card>
            )}
          </aside>
        </div>

        <p className="mt-8 flex justify-between border-t border-rule pt-3 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
          <span>job · {schedule.name}</span>
          <span>{runsQ.isLoading ? 'loading runs…' : 'live'}</span>
        </p>
      </div>

      {editing && (
        <ScheduleOverrideForm
          schedule={schedule}
          onClose={() => setEditing(false)}
        />
      )}
    </>
  )
}

/* --------------------- helpers --------------------- */

function RunRow({ run: r }: { run: RunSummary }) {
  const { variant, label } = pillFromRunStatus(r.status)
  const isFailing = r.status === 'failed' || r.status === 'partial_failure'
  return (
    <tr
      className={cn(
        'border-b border-rule transition-colors hover:bg-bg-3 last:border-b-0',
        isFailing && 'shadow-[inset_2px_0_0_var(--red)]',
      )}
    >
      <td className="px-4 py-2.5 text-ink-3">
        <Link to={`/runs/${r.id}`} className="hover:text-accent-cyan">
          #{r.id.slice(0, 8)}
        </Link>
      </td>
      <td className="px-4 py-2.5">
        <Pill variant={variant}>{r.error_kind ?? label}</Pill>
      </td>
      <td className="px-4 py-2.5 text-ink-2">
        {r.started_at
          ? new Date(r.started_at).toLocaleString(undefined, {
              dateStyle: 'medium',
              timeStyle: 'medium',
            })
          : '—'}
      </td>
      <td className="px-4 py-2.5 text-ink-2">
        {r.duration_ms != null ? formatDuration(r.duration_ms) : '—'}
      </td>
      <td className="px-4 py-2.5 text-right text-ink-2">
        {r.fire_reason}
        {r.actor ? ` · ${r.actor}` : ''}
      </td>
    </tr>
  )
}

function Sparkline({ runs }: { runs: RunSummary[] }) {
  // Render newest on the right so the sparkline reads left → right
  // chronologically, matching the table below it.
  const ordered = [...runs].reverse()
  const maxMs = Math.max(
    1,
    ...ordered.map((r) => r.duration_ms ?? 0),
  )
  return (
    <div className="flex items-end gap-1 px-4" style={{ height: 38 }}>
      {ordered.map((r) => {
        const isFail =
          r.status === 'failed' || r.status === 'partial_failure'
        const h = r.duration_ms != null ? (r.duration_ms / maxMs) * 100 : 30
        return (
          <span
            key={r.id}
            className={cn(
              'min-h-[2px] flex-1 rounded-sm',
              isFail
                ? 'bg-accent-red shadow-[0_0_6px_rgba(255,93,82,0.5)]'
                : 'bg-accent-green-dim',
            )}
            style={{ height: `${Math.max(8, h)}%` }}
            aria-hidden="true"
          />
        )
      })}
    </div>
  )
}

function medianDuration(runs: RunSummary[]): string {
  const xs = runs
    .map((r) => r.duration_ms)
    .filter((v): v is number => v != null && v >= 0)
    .sort((a, b) => a - b)
  if (xs.length === 0) return ''
  const mid = xs[Math.floor(xs.length / 2)]
  return `median ${formatDuration(mid)}`
}

/**
 * humanizeCron — best-effort plain-English description for the most
 * common patterns. Falls back to the raw cron when we don't recognise
 * the shape; better to be silent than wrong.
 */
function humanizeCron(cron: string): string {
  const parts = cron.trim().split(/\s+/)
  if (parts.length !== 5) return cron
  const [m, h, dom, mon, dow] = parts
  if (m === '*' && h === '*' && dom === '*' && mon === '*' && dow === '*') {
    return 'every minute'
  }
  if (m === '0' && h === '*' && dom === '*' && mon === '*' && dow === '*') {
    return 'every hour at :00'
  }
  if (m === '0' && /^\d+$/.test(h) && dom === '*' && mon === '*' && dow === '*') {
    return `every day at ${h.padStart(2, '0')}:00`
  }
  if (
    m === '0' &&
    /^\d+$/.test(h) &&
    dom === '*' &&
    mon === '*' &&
    /^[0-6]$/.test(dow)
  ) {
    const dows = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat']
    return `every ${dows[parseInt(dow, 10)]} at ${h.padStart(2, '0')}:00`
  }
  if (m.startsWith('*/')) {
    return `every ${m.slice(2)} minutes`
  }
  return cron
}
