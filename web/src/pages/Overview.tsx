// web/src/pages/Overview.tsx
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import {
  activityByHour,
  failingSchedules,
  formatCountdown,
  schedulesByProvider,
  upcomingRuns,
} from '../lib/derived'
import { relativeTime } from '../lib/time'
import {
  Button,
  Card,
  IconButton,
  KV,
  PageHeader,
  Pill,
  Topbar,
} from '../components/ui'
import { cn } from '../lib/cn'
import type { Alerts } from '../lib/types'

/**
 * Overview — triage home. Operator opens this page first thing in
 * the morning to answer "is anything broken?". Layout priorities:
 *
 *   1. Failures-first (top of page, can't be missed).
 *   2. What's running soon (planning context).
 *   3. 24h activity heatmap (pattern recognition over time).
 *   4. Side rail: derivable system context + speculative cards
 *      marked "coming soon" until their endpoints land.
 *
 * The page is data-light by design — every card answers a single
 * question. Anything that needs >1 click to act on belongs on a
 * detail page, not here.
 */
export default function Overview() {
  const qc = useQueryClient()

  const schedulesQ = useQuery({
    queryKey: ['schedules'],
    queryFn: api.schedules.list,
    refetchInterval: 30_000,
  })
  const recentRunsQ = useQuery({
    queryKey: ['runs', { limit: 200 }],
    queryFn: () => api.runs.list({ limit: 200 }),
    refetchInterval: 15_000,
  })
  const healthQ = useQuery({
    queryKey: ['system', 'health'],
    queryFn:  api.system.health,
    refetchInterval: 30_000,
  })
  const alertsQ = useQuery({
    queryKey: ['alerts'],
    queryFn:  api.alerts.list,
    refetchInterval: 30_000,
  })

  const runNow = useMutation({
    mutationFn: api.schedules.runNow,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['runs'] }),
  })
  const pause = useMutation({
    mutationFn: api.schedules.pause,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })

  const schedules = schedulesQ.data ?? []
  const runs = recentRunsQ.data ?? []
  const failing = failingSchedules(schedules, runs)
  const upcoming = upcomingRuns(schedules).slice(0, 6)
  const hours = activityByHour(runs)
  const providers = schedulesByProvider(schedules)

  // Activity tally for the card header — independent of the heatmap
  // bucketing so the displayed number matches what the user sees.
  const totalLast24h = hours.reduce((n, b) => n + b.total, 0)
  const okLast24h = hours.reduce((n, b) => n + b.succeeded, 0)
  const failLast24h = hours.reduce((n, b) => n + b.failed, 0)
  const skipLast24h = totalLast24h - okLast24h - failLast24h

  const eyebrow =
    failing.length > 0
      ? ({
          tone: 'fail',
          text: `${failing.length} job${failing.length === 1 ? '' : 's'} failing · attention required`,
        } as const)
      : ({
          tone: 'ok',
          text: `${schedules.length} jobs · all healthy`,
        } as const)

  const greeting = greetingFor(new Date())

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Here>Overview</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[1320px] px-6 pb-16 pt-7">
        <PageHeader
          eyebrow={eyebrow}
          title={`${greeting}, you`}
          subtitle={`${formatNowUtc(new Date())} · ${schedules.length} jobs · ${providers.length} providers`}
          actions={
            <>
              <Button variant="primary" shortcut="N">+ New job</Button>
              <Button>Run a job</Button>
            </>
          }
        />

        {/* ---------- FAILURE TRIAGE ---------- */}
        {failing.length > 0 && (
          <Card className="border-accent-red-dim">
            <Card.Header className="border-b-accent-red-dim bg-red-bg">
              <span className="text-accent-red">● Failing now</span>
              <Card.HeaderMeta>
                {failing.length} jobs · oldest{' '}
                {failing[0].lastRun.created_at &&
                  relativeTime(failing[0].lastRun.created_at)}
              </Card.HeaderMeta>
            </Card.Header>
            <ul className="m-0 list-none p-0">
              {failing.map(({ schedule, lastRun, streak }) => (
                <li
                  key={schedule.id}
                  className={cn(
                    'grid grid-cols-[120px_220px_1fr_auto_auto] items-center gap-4 border-b border-rule px-5 py-3.5 last:border-b-0',
                    'transition-colors hover:bg-[rgba(255,93,82,0.04)]',
                  )}
                >
                  <div className="flex flex-col gap-1">
                    <Pill variant="fail">
                      {lastRun.error_kind ?? lastRun.status}
                    </Pill>
                    {streak > 1 && (
                      <span className="font-mono text-[10px] tracking-[0.06em] text-accent-red">
                        {streak} in a row
                      </span>
                    )}
                  </div>
                  <div>
                    <Link
                      to={`/jobs/${schedule.id}`}
                      className="text-[14px] font-semibold text-ink hover:text-accent-green"
                    >
                      {schedule.name}
                    </Link>
                    <span className="mt-0.5 block font-mono text-[11px] text-ink-3">
                      {schedule.owner}/{schedule.repo_name} ·{' '}
                      {schedule.skill_path}
                    </span>
                  </div>
                  <div className="min-w-0 truncate font-mono text-[12px] text-ink-2">
                    {lastRun.error_msg ?? '(no message)'}
                  </div>
                  <div className="text-right font-mono text-[10px] uppercase tracking-[0.1em] text-ink-3">
                    <b className="block text-[13px] normal-case tracking-normal text-ink">
                      {lastRun.created_at && relativeTime(lastRun.created_at)}
                    </b>
                    <span>
                      {lastRun.fire_reason} · {schedule.cron}
                    </span>
                  </div>
                  <div className="flex gap-1">
                    {/* Inline icon-link to the filtered runs feed —
                        styled to match IconButton but rendered as an
                        anchor since it's pure navigation, not an
                        action. Avoids nesting <button> in <a>. */}
                    <Link
                      to={`/runs?schedule_id=${schedule.id}`}
                      aria-label={`View runs for ${schedule.name}`}
                      title="View runs"
                      className="inline-flex h-7 w-7 items-center justify-center rounded border border-rule-2 bg-bg-3 text-[11px] text-ink-2 transition-colors hover:border-ink-3 hover:bg-bg-4 hover:text-ink"
                    >
                      ☰
                    </Link>
                    <IconButton
                      aria-label={`Re-run ${schedule.name}`}
                      onClick={() => runNow.mutate(schedule.id)}
                      disabled={runNow.isPending}
                      title="Re-run now"
                    >
                      ▶
                    </IconButton>
                    <IconButton
                      aria-label={`Pause ${schedule.name}`}
                      onClick={() => pause.mutate(schedule.id)}
                      disabled={pause.isPending}
                      title="Pause"
                    >
                      ⏸
                    </IconButton>
                  </div>
                </li>
              ))}
            </ul>
          </Card>
        )}

        {/* ---------- TWO COLUMNS ---------- */}
        <div className="grid grid-cols-[1fr_320px] gap-5">
          <div className="min-w-0">
            {/* Upcoming */}
            <Card>
              <Card.Header>
                <span>Upcoming runs</span>
                <Card.HeaderMeta>
                  {upcoming.length === 0 ? 'nothing scheduled' : `next ${upcoming.length}`}
                </Card.HeaderMeta>
              </Card.Header>
              {upcoming.length === 0 ? (
                <Card.Body>
                  <p className="m-0 font-mono text-[12px] text-ink-3">
                    No scheduled runs in the foreseeable future. Pause status
                    or invalid cron?
                  </p>
                </Card.Body>
              ) : (
                <ul className="m-0 list-none p-0">
                  {upcoming.map(({ schedule, msUntil }) => (
                    <li
                      key={schedule.id}
                      className="grid grid-cols-[140px_110px_1fr_auto] items-center gap-3.5 border-b border-rule px-4 py-2.5 text-[13px] last:border-b-0 hover:bg-bg-3"
                    >
                      <span className="flex flex-col gap-0.5 font-mono text-[10px] uppercase tracking-[0.1em] text-ink-3">
                        <b className="text-[12px] font-medium normal-case tracking-normal text-ink">
                          {formatCountdown(msUntil)}
                        </b>
                        <span>{formatLocalShort(schedule.next_fire_at!)}</span>
                      </span>
                      <UpcomingBar msUntil={msUntil} />
                      <span>
                        <Link
                          to={`/jobs/${schedule.id}`}
                          className="border-b border-dotted border-ink-4 font-medium text-ink hover:border-accent-green hover:text-accent-green"
                        >
                          {schedule.name}
                        </Link>
                        <span className="ml-1 font-mono text-ink-3">
                          · {schedule.cron}
                        </span>
                      </span>
                      <span className="text-[11px] text-ink-3">
                        {schedule.provider}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </Card>

            {/* Activity heatmap */}
            <Card>
              <Card.Header>
                <span>Activity · last 24h</span>
                <Card.HeaderMeta>
                  {totalLast24h} runs · {okLast24h} ok · {failLast24h} fail
                  {skipLast24h > 0 && ` · ${skipLast24h} other`}
                </Card.HeaderMeta>
                <Link
                  to="/runs"
                  className="font-mono text-[11px] tracking-[0.04em] text-ink-2 hover:text-accent-green"
                >
                  Run log →
                </Link>
              </Card.Header>
              <ActivityHeatmap hours={hours} />
            </Card>
          </div>

          {/* ---------- RIGHT RAIL ---------- */}
          <aside className="min-w-0">
            {/* Providers — fully derivable */}
            <Card>
              <Card.Header>
                <span>Providers</span>
                <Card.HeaderMeta>{providers.length}</Card.HeaderMeta>
              </Card.Header>
              <ul className="m-0 list-none p-0 text-[12px]">
                {providers.map((p) => (
                  <li
                    key={p.provider}
                    className="flex items-center gap-2.5 border-b border-rule px-4 py-2 last:border-b-0"
                  >
                    <Pill variant={p.activeCount > 0 ? 'ok' : 'skip'} />
                    <span className="font-mono text-ink">{p.provider}</span>
                    <span className="ml-auto font-mono text-[11px] text-ink-3">
                      {p.activeCount}/{p.jobCount} active
                    </span>
                  </li>
                ))}
              </ul>
            </Card>

            {/* System health */}
            <Card>
              <Card.Header>System</Card.Header>
              <Card.Body>
                <KV>
                  <KV.Row label="Scheduler">
                    <Pill variant={
                      healthQ.data?.scheduler.status === 'healthy' ? 'ok'
                      : healthQ.data?.scheduler.status === 'degraded' ? 'amber'
                      : 'fail'
                    }>
                      {healthQ.data?.scheduler.status ?? '—'}
                    </Pill>
                  </KV.Row>
                  <KV.Row label="Queue depth">
                    {healthQ.data ? `${healthQ.data.queue_depth} pending` : '—'}
                  </KV.Row>
                  <KV.Row label="Workers">
                    {healthQ.data ? String(healthQ.data.workers) : '—'}
                  </KV.Row>
                  <KV.Row label="Last sync">
                    {healthQ.data?.last_sync_at ? relativeTime(healthQ.data.last_sync_at) : '—'}
                  </KV.Row>
                </KV>
              </Card.Body>
            </Card>

            {/* Alerts */}
            <Card>
              <Card.Header>Alerts &amp; rotations</Card.Header>
              <Card.Body>
                <AlertsList data={alertsQ.data} />
              </Card.Body>
            </Card>
          </aside>
        </div>

        <p className="mt-8 flex justify-between border-t border-rule pt-3 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
          <span>overview · {schedules.length} jobs</span>
          <span>{schedulesQ.isLoading ? 'loading…' : 'live'}</span>
        </p>
      </div>
    </>
  )
}

/* --------------------- helpers --------------------- */

function AlertsList({ data }: { data?: Alerts }) {
  if (!data) return <p className="text-ink-3">—</p>
  type Item = { tone: 'amber' | 'fail'; label: string; text: string; sub?: string }
  const items: Item[] = [
    ...data.quiet_jobs.map((q): Item => ({
      tone: 'amber',
      label: 'quiet',
      text: `${q.schedule_name} hasn't succeeded`,
      sub:  q.last_success ? `last ok ${relativeTime(q.last_success)}` : 'never succeeded',
    })),
    ...data.recently_paused.map((p): Item => ({
      tone: 'fail',
      label: 'paused',
      text: `${p.schedule_name} auto-paused`,
      sub:  p.reason ?? undefined,
    })),
  ]
  if (items.length === 0) return <p className="text-ink-3">All quiet.</p>
  return (
    <ul className="m-0 flex list-none flex-col gap-3 p-0 text-[12px]">
      {items.map((it, i) => (
        <li key={i} className="flex gap-3">
          <Pill variant={it.tone}>{it.label}</Pill>
          <span>
            {it.text}
            {it.sub && (
              <span className="mt-0.5 block font-mono text-[10px] uppercase tracking-[0.06em] text-ink-3">
                {it.sub}
              </span>
            )}
          </span>
        </li>
      ))}
    </ul>
  )
}

function greetingFor(d: Date): string {
  const h = d.getHours()
  if (h < 5) return 'Good night'
  if (h < 12) return 'Good morning'
  if (h < 18) return 'Good afternoon'
  return 'Good evening'
}

function formatNowUtc(d: Date): string {
  // "Friday, 03 May · 02:03 UTC" — both halves must be UTC; using the
  // local timezone for the date drifts when the viewer crosses
  // midnight (e.g., late-night PST shows the next-day weekday).
  const day = d.toLocaleDateString('en-GB', {
    weekday: 'long',
    day: '2-digit',
    month: 'short',
    timeZone: 'UTC',
  })
  const hh = d.getUTCHours().toString().padStart(2, '0')
  const mm = d.getUTCMinutes().toString().padStart(2, '0')
  return `${day} · ${hh}:${mm} UTC`
}

function formatLocalShort(iso: string): string {
  // "tomorrow 02:00" or "fri 09:00" — just enough context to scan.
  const d = new Date(iso)
  const today = new Date()
  const tomorrow = new Date(today)
  tomorrow.setDate(today.getDate() + 1)
  const hh = d.getHours().toString().padStart(2, '0')
  const mm = d.getMinutes().toString().padStart(2, '0')
  if (sameDay(d, today)) return `today ${hh}:${mm}`
  if (sameDay(d, tomorrow)) return `tomorrow ${hh}:${mm}`
  const dow = d.toLocaleDateString('en-US', { weekday: 'short' }).toLowerCase()
  return `${dow} ${hh}:${mm}`
}

function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  )
}

interface BarProps {
  msUntil: number
}
/** Visualises how far in the future a run is on a normalised 0..7d scale. */
function UpcomingBar({ msUntil }: BarProps) {
  const sevenDays = 7 * 24 * 60 * 60 * 1000
  const pct = Math.max(1, Math.min(95, (msUntil / sevenDays) * 100))
  return (
    <span className="relative block h-1 overflow-hidden rounded-sm bg-bg-4">
      <span
        className="absolute top-0 h-full w-2 rounded-sm bg-accent-cyan shadow-[0_0_6px_rgba(94,200,214,0.4)]"
        style={{ left: `${pct}%` }}
        aria-hidden="true"
      />
    </span>
  )
}

interface HeatmapProps {
  hours: ReturnType<typeof activityByHour>
}
function ActivityHeatmap({ hours }: HeatmapProps) {
  // Visual rule: failures dominate the eye (red glow), successes
  // fade with intensity. Skipped/empty hours render as the bg-4 base.
  return (
    <>
      <div
        className="grid gap-[3px] px-4 pb-1.5 pt-3.5"
        style={{ gridTemplateColumns: 'repeat(24, 1fr)' }}
        aria-label="Hourly activity for the last 24 hours"
      >
        {hours.map((b, i) => {
          const tone =
            b.failed > 0
              ? 'bg-accent-red shadow-[0_0_8px_rgba(255,93,82,0.4)]'
              : b.succeeded > 0
                ? 'bg-accent-green-dim'
                : b.total > 0
                  ? 'bg-ink-4'
                  : 'bg-bg-4'
          // Opacity scales with run count so quiet hours stay subtle.
          const opacity = b.total === 0 ? 0.25 : Math.min(1, 0.4 + b.total * 0.2)
          return (
            <div
              key={i}
              className="flex flex-col items-center gap-1"
              title={`${b.total} runs · ${b.succeeded} ok · ${b.failed} fail`}
            >
              <span
                className={cn('h-9 w-full rounded-sm', tone)}
                style={{ opacity }}
              />
              <span className="font-mono text-[9px] text-ink-4">
                {b.hour.toString().padStart(2, '0')}
              </span>
            </div>
          )
        })}
      </div>
      <div className="flex items-center gap-3.5 px-4 pb-3.5 pt-2 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
        <span>
          <span className="mr-1.5 inline-block h-2 w-2 rounded-sm bg-accent-green-dim align-middle" />
          success
        </span>
        <span>
          <span className="mr-1.5 inline-block h-2 w-2 rounded-sm bg-accent-red align-middle" />
          failure
        </span>
        <span>
          <span className="mr-1.5 inline-block h-2 w-2 rounded-sm bg-ink-4 align-middle" />
          other
        </span>
        <span className="ml-auto">all times local</span>
      </div>
    </>
  )
}
