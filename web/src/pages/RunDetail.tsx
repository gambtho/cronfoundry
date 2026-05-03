// web/src/pages/RunDetail.tsx
import { Link, useParams } from 'react-router-dom'
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
import LogTail from '../components/LogTail'
import { PhaseBar } from '../components/run/PhaseBar'
import { formatDuration } from '../lib/derived'
import { relativeTime } from '../lib/time'
import { cn } from '../lib/cn'
import type { RunDetail, RunSummary } from '../lib/types'

/**
 * RunDetail — single-run forensics. Designed to answer "what
 * happened?" without leaving the page:
 *
 *   - 5-column meta strip (id-shape glance: status, duration, provider,
 *     attempt, when).
 *   - Failure card surfaces the error first when the run failed; the
 *     full log tail lives below.
 *   - Right rail links back to the parent Job and forward to related
 *     runs of the same schedule.
 *   - Segmented phase bar (boot → secrets → exec → publish) sits between
 *     the meta strip and the body, fed by `runs.events` phase tags.
 */
export default function RunDetailPage() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()

  const runQ = useQuery({
    queryKey: ['run', id],
    queryFn: () => api.runs.get(id!),
    enabled: !!id,
    refetchInterval: (q) => {
      const data = q.state.data as RunDetail | undefined
      // Stop polling once the run reaches a terminal state.
      if (
        data &&
        (data.status === 'succeeded' ||
          data.status === 'failed' ||
          data.status === 'partial_failure')
      ) {
        return false
      }
      return 5_000
    },
  })

  // Fetch a small slice of sibling runs so we can link "previous
  // failure" / "next run" affordances.
  const siblingsQ = useQuery({
    queryKey: ['runs', { schedule_id: runQ.data?.schedule_name, limit: 30 }],
    // We only have schedule_name on the run; the API filter takes
    // schedule_id. We still get the name list cheaply enough for a
    // lookup against schedules.
    queryFn: () => api.runs.list({ limit: 30 }),
    enabled: !!runQ.data,
  })

  const isTerminal =
    runQ.data?.status === 'succeeded' ||
    runQ.data?.status === 'failed' ||
    runQ.data?.status === 'partial_failure'

  const notifsQ = useQuery({
    queryKey: ['run', id, 'notifications'],
    queryFn: () => api.runs.notifications(id!),
    enabled: !!id && isTerminal,
  })

  // Schedules already cached by Layout — this is effectively free.
  const schedulesQ = useQuery({
    queryKey: ['schedules'],
    queryFn: api.schedules.list,
  })

  // Run events power the segmented phase bar. Poll while the run is
  // active; stop once it reaches a terminal state.
  const eventsQ = useQuery({
    queryKey: ['run', id, 'events'],
    queryFn: () => api.runs.events(id!),
    enabled: !!id,
    refetchInterval: () => {
      const data = runQ.data
      if (
        data &&
        (data.status === 'succeeded' ||
          data.status === 'failed' ||
          data.status === 'partial_failure')
      ) {
        return false
      }
      return 5_000
    },
  })

  const reRun = useMutation({
    // Optimistic: kicks the schedule, not literally this run.
    mutationFn: (scheduleId: string) => api.schedules.runNow(scheduleId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['runs'] }),
  })

  if (!id) {
    return <div className="p-6 font-mono text-ink-3">No run id.</div>
  }

  if (runQ.isLoading) {
    return (
      <>
        <Topbar>
          <Topbar.Crumbs>
            <Topbar.Crumb href="/jobs">Jobs</Topbar.Crumb>
            <Topbar.Sep />
            <Topbar.Here>loading…</Topbar.Here>
          </Topbar.Crumbs>
          <Topbar.Spacer />
          <Topbar.Search />
        </Topbar>
        <div className="px-6 py-12 text-center font-mono text-[12px] text-ink-3">
          Loading run…
        </div>
      </>
    )
  }

  if (runQ.isError || !runQ.data) {
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
          Run not found.{' '}
          <Link to="/jobs" className="text-ink hover:text-accent-green">
            Back to jobs →
          </Link>
        </div>
      </>
    )
  }

  const run = runQ.data
  const isFailing =
    run.status === 'failed' || run.status === 'partial_failure'

  // Find the parent schedule so we can deep-link to /jobs/:id rather
  // than relying on schedule_name (which the API gives us) being
  // URL-safe. schedulesQ above is shared with Layout.
  const schedule = schedulesQ.data?.find((s) => s.name === run.schedule_name)

  // Previous failing run for this schedule, excluding the current one.
  const previousFailure: RunSummary | undefined = (siblingsQ.data ?? [])
    .filter(
      (r) =>
        r.id !== run.id &&
        r.schedule_name === run.schedule_name &&
        (r.status === 'failed' || r.status === 'partial_failure'),
    )
    .sort((a, b) => (a.created_at < b.created_at ? 1 : -1))[0]

  const eyebrow = isFailing
    ? ({
        tone: 'fail',
        text: `Failed · ${run.error_kind ?? run.status}${
          run.duration_ms != null ? ` · ${formatDuration(run.duration_ms)}` : ''
        }`,
      } as const)
    : run.status === 'running'
      ? ({ tone: 'warn', text: 'Running' } as const)
      : ({
          tone: 'ok',
          text: `${pillFromRunStatus(run.status).label}${
            run.duration_ms != null ? ` · ${formatDuration(run.duration_ms)}` : ''
          }`,
        } as const)

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Crumb href="/jobs">Jobs</Topbar.Crumb>
          <Topbar.Sep />
          {schedule ? (
            <Topbar.Crumb href={`/jobs/${schedule.id}`}>
              {run.schedule_name}
            </Topbar.Crumb>
          ) : (
            <Topbar.Here>{run.schedule_name}</Topbar.Here>
          )}
          <Topbar.Sep />
          <Topbar.Here>run #{run.id.slice(0, 8)}</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[1320px] px-6 pb-16 pt-7">
        <PageHeader
          eyebrow={eyebrow}
          title={`run #${run.id.slice(0, 8)}`}
          subtitle={
            <>
              {schedule ? (
                <Link
                  to={`/jobs/${schedule.id}`}
                  className="border-b border-dotted border-ink-3 text-ink hover:text-accent-green"
                >
                  {run.schedule_name}
                </Link>
              ) : (
                <span>{run.schedule_name}</span>
              )}
              {' · '}
              {run.started_at
                ? new Date(run.started_at).toLocaleString(undefined, {
                    dateStyle: 'medium',
                    timeStyle: 'medium',
                  })
                : 'not yet started'}
            </>
          }
          actions={
            <>
              {schedule && (
                <Button
                  variant="primary"
                  shortcut="R"
                  onClick={() => reRun.mutate(schedule.id)}
                  disabled={reRun.isPending}
                >
                  ↻ Re-run
                </Button>
              )}
              {schedule && (
                <Link to={`/jobs/${schedule.id}`}>
                  <Button>Open job</Button>
                </Link>
              )}
            </>
          }
        >
          <PageHeader.Meta>
            <PageHeader.Chip>trigger: {run.fire_reason}</PageHeader.Chip>
            {run.actor && <PageHeader.Chip>by {run.actor}</PageHeader.Chip>}
            {previousFailure && (
              <span>
                same kind of failure as{' '}
                <Link
                  to={`/runs/${previousFailure.id}`}
                  className="border-b border-dotted border-accent-red-dim text-accent-red hover:underline"
                >
                  #{previousFailure.id.slice(0, 8)}
                </Link>
              </span>
            )}
          </PageHeader.Meta>
        </PageHeader>

        {/* meta strip */}
        <dl className="mb-4 grid grid-cols-5 gap-0 rounded border border-rule bg-bg-2">
          <MetaCell label="Status">
            {(() => {
              const { variant, label } = pillFromRunStatus(run.status)
              return <Pill variant={variant}>{run.error_kind ?? label}</Pill>
            })()}
          </MetaCell>
          <MetaCell label="Duration">
            {run.duration_ms != null ? formatDuration(run.duration_ms) : '—'}
          </MetaCell>
          <MetaCell label="Provider">
            {schedule?.provider ?? '—'}
          </MetaCell>
          <MetaCell label="Cost">
            {run.cost_cents != null
              ? `$${(run.cost_cents / 100).toFixed(2)}`
              : '—'}
          </MetaCell>
          <MetaCell label="Tokens">
            {run.tokens_in != null && run.tokens_out != null
              ? `${run.tokens_in.toLocaleString()} → ${run.tokens_out.toLocaleString()}`
              : '—'}
          </MetaCell>
        </dl>

        <PhaseBar events={eventsQ.data ?? []} finishedAt={run.finished_at} />

        <div className="grid grid-cols-[1fr_320px] gap-5">
          <div className="min-w-0">
            {/* failure detail */}
            {isFailing && (
              <Card className="border-accent-red-dim">
                <Card.Header className="border-b-accent-red-dim bg-red-bg">
                  <span className="text-accent-red">
                    ● {run.error_kind ? `Error · ${run.error_kind}` : 'Error'}
                  </span>
                </Card.Header>
                <div className="p-0">
                  <pre
                    className={cn(
                      'm-0 overflow-x-auto bg-[#0d0707] p-3.5 font-mono text-[11.5px] leading-[1.7]',
                    )}
                  >
                    <span className="text-accent-red">
                      {run.error_msg ?? '(no message)'}
                    </span>
                  </pre>
                </div>
                <div className="flex flex-wrap gap-2 border-t border-accent-red-dim p-3.5">
                  {schedule && (
                    <Button
                      onClick={() => reRun.mutate(schedule.id)}
                      disabled={reRun.isPending}
                    >
                      ↻ Re-run
                    </Button>
                  )}
                  {previousFailure && (
                    <Link to={`/runs/${previousFailure.id}`}>
                      <Button variant="ghost">
                        View previous failure →
                      </Button>
                    </Link>
                  )}
                </div>
              </Card>
            )}

            {/* logs */}
            <Card>
              <Card.Header>
                <span>Logs</span>
                <Card.HeaderMeta>stdout + stderr · live tail</Card.HeaderMeta>
              </Card.Header>
              <div className="bg-[#06080a] p-0">
                <LogTail runId={run.id} status={run.status} />
              </div>
            </Card>
          </div>

          {/* RIGHT RAIL */}
          <aside className="min-w-0">
            {schedule && (
              <Card>
                <Card.Header>Job</Card.Header>
                <Card.Body>
                  <KV>
                    <KV.Row label="Name">
                      <Link to={`/jobs/${schedule.id}`}>
                        {run.schedule_name}
                      </Link>
                    </KV.Row>
                    <KV.Row label="Schedule">{schedule.cron}</KV.Row>
                    <KV.Row label="Repo">
                      {schedule.owner}/{schedule.repo_name}
                    </KV.Row>
                    <KV.Row label="File">{schedule.skill_path}</KV.Row>
                  </KV>
                </Card.Body>
              </Card>
            )}

            <Card>
              <Card.Header>Timing</Card.Header>
              <Card.Body>
                <KV>
                  <KV.Row label="Created">
                    {relativeTime(run.created_at)}
                  </KV.Row>
                  <KV.Row label="Started">
                    {run.started_at ? relativeTime(run.started_at) : '—'}
                  </KV.Row>
                  <KV.Row label="Finished">
                    {run.finished_at ? relativeTime(run.finished_at) : '—'}
                  </KV.Row>
                </KV>
              </Card.Body>
            </Card>

            {isTerminal && (
              <Card>
                <Card.Header>Notifications sent</Card.Header>
                <Card.Body>
                  {notifsQ.isLoading ? (
                    <p className="text-ink-3">Loading…</p>
                  ) : notifsQ.error ? (
                    <p className="text-ink-3">Failed to load notifications.</p>
                  ) : (notifsQ.data ?? []).length === 0 ? (
                    <p className="text-ink-3">No destinations configured for this run.</p>
                  ) : (
                    <ul className="m-0 flex list-none flex-col gap-2 p-0 text-[12px]">
                      {notifsQ.data!.map((n) => (
                        <li key={n.id} className="flex flex-col gap-0.5">
                          <div className="flex items-center gap-2">
                            <Pill
                              variant={
                                n.status === 'sent'
                                  ? 'ok'
                                  : n.status === 'skipped'
                                    ? 'skip'
                                    : 'fail'
                              }
                            >
                              {n.status}
                            </Pill>
                            <span className="font-mono">{n.kind}</span>
                            <span className="ml-auto font-mono text-ink-3">
                              {n.target}
                            </span>
                          </div>
                          {n.reason && (
                            <span className="italic text-ink-3">{n.reason}</span>
                          )}
                        </li>
                      ))}
                    </ul>
                  )}
                </Card.Body>
              </Card>
            )}

            {run.writeback_commit_sha && (
              <Card>
                <Card.Header>Writeback</Card.Header>
                <Card.Body>
                  <KV>
                    <KV.Row label="Commit">
                      {run.writeback_commit_sha.slice(0, 7)}
                    </KV.Row>
                  </KV>
                </Card.Body>
              </Card>
            )}
          </aside>
        </div>

        <p className="mt-8 flex justify-between border-t border-rule pt-3 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
          <span>run · {run.id.slice(0, 8)}</span>
          <span>
            {run.status === 'running' ? 'streaming' : run.status}
          </span>
        </p>
      </div>
    </>
  )
}

function MetaCell({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="border-r border-rule px-4 py-3.5 last:border-r-0">
      <dt className="mb-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-ink-3">
        {label}
      </dt>
      <dd className="m-0 font-mono text-[13px] font-medium text-ink">
        {children}
      </dd>
    </div>
  )
}
