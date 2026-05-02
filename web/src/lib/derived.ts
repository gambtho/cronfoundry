// web/src/lib/derived.ts
//
// Pure helpers that derive UI-shaped data from raw API responses.
// Keeping them pure (no React, no fetch) makes them easy to unit-test
// and reuse across pages without re-running expensive logic in render.

import type { RunStatus, RunSummary, Schedule } from './types'

/**
 * The most recent run we've seen for each schedule, keyed by schedule_name.
 * `runs` is assumed to come back from the API in newest-first order
 * (which `runs.list()` does), so the first occurrence wins.
 */
export function lastRunByScheduleName(
  runs: RunSummary[],
): Map<string, RunSummary> {
  const out = new Map<string, RunSummary>()
  for (const r of runs) {
    if (!out.has(r.schedule_name)) {
      out.set(r.schedule_name, r)
    }
  }
  return out
}

/** Statuses that count as "needs operator attention". */
const FAILING_STATUSES = new Set<RunStatus>(['failed', 'partial_failure'])

/** Schedules whose most recent run is failing, with the failing run attached. */
export interface FailingSchedule {
  schedule: Schedule
  lastRun: RunSummary
  /** How many consecutive failed/partial runs at the head of the run history. */
  streak: number
}

export function failingSchedules(
  schedules: Schedule[],
  runs: RunSummary[],
): FailingSchedule[] {
  // Bucket runs per schedule, preserving newest-first order.
  const runsBySchedule = new Map<string, RunSummary[]>()
  for (const r of runs) {
    const list = runsBySchedule.get(r.schedule_name) ?? []
    list.push(r)
    runsBySchedule.set(r.schedule_name, list)
  }

  const out: FailingSchedule[] = []
  for (const s of schedules) {
    const list = runsBySchedule.get(s.name) ?? []
    if (list.length === 0) continue
    const head = list[0]
    if (!FAILING_STATUSES.has(head.status)) continue

    let streak = 0
    for (const r of list) {
      if (FAILING_STATUSES.has(r.status)) streak++
      else break
    }
    out.push({ schedule: s, lastRun: head, streak })
  }
  // Sort by streak length so the most-broken jobs surface first.
  return out.sort((a, b) => b.streak - a.streak)
}

/**
 * Schedules sorted by next_fire_at ascending. Schedules with no
 * scheduled next run (paused, no cron) are dropped.
 */
export interface UpcomingRun {
  schedule: Schedule
  fireAt: Date
  msUntil: number
}

export function upcomingRuns(
  schedules: Schedule[],
  now: Date = new Date(),
): UpcomingRun[] {
  const nowMs = now.getTime()
  const out: UpcomingRun[] = []
  for (const s of schedules) {
    if (!s.next_fire_at) continue
    const fireAt = new Date(s.next_fire_at)
    const msUntil = fireAt.getTime() - nowMs
    // Past-due fire times can happen briefly while the scheduler
    // catches up; treat them as imminent rather than dropping them.
    out.push({ schedule: s, fireAt, msUntil: Math.max(0, msUntil) })
  }
  return out.sort((a, b) => a.msUntil - b.msUntil)
}

/**
 * 24-bucket histogram of runs by hour-of-day in the user's local
 * timezone. Each bucket counts how many runs of each status started
 * during that hour, looking back `windowHours` from `now`.
 */
export interface HourBucket {
  hour: number // 0..23 (local)
  succeeded: number
  failed: number
  skipped: number
  running: number
  pending: number
  /** Sum across all statuses. */
  total: number
}

export function activityByHour(
  runs: RunSummary[],
  windowHours = 24,
  now: Date = new Date(),
): HourBucket[] {
  // Initialise 24 buckets ordered chronologically: oldest hour first,
  // most-recent hour last. The bucket at index N represents the hour
  // that starts (windowHours - 1 - N) hours before `now`.
  const buckets: HourBucket[] = []
  const cursor = new Date(now)
  cursor.setMinutes(0, 0, 0)
  for (let i = windowHours - 1; i >= 0; i--) {
    const slot = new Date(cursor.getTime() - i * 60 * 60 * 1000)
    buckets.push({
      hour: slot.getHours(),
      succeeded: 0,
      failed: 0,
      skipped: 0,
      running: 0,
      pending: 0,
      total: 0,
    })
  }

  const cutoffMs = cursor.getTime() - (windowHours - 1) * 60 * 60 * 1000
  for (const r of runs) {
    if (!r.started_at) continue
    const t = new Date(r.started_at).getTime()
    if (t < cutoffMs) continue
    const idx = Math.floor((t - cutoffMs) / (60 * 60 * 1000))
    if (idx < 0 || idx >= buckets.length) continue
    const b = buckets[idx]
    b.total++
    switch (r.status) {
      case 'succeeded':
        b.succeeded++
        break
      case 'failed':
      case 'partial_failure':
        b.failed++
        break
      case 'pending':
        b.pending++
        break
      case 'running':
        b.running++
        break
    }
  }
  return buckets
}

/**
 * Format a millisecond duration as a short countdown string.
 *   < 1m   → "now"
 *   < 1h   → "12m"
 *   < 1d   → "4h 12m"
 *   else   → "3d 7h"
 */
export function formatCountdown(ms: number): string {
  const totalMin = Math.floor(ms / 60_000)
  if (totalMin < 1) return 'now'
  if (totalMin < 60) return `${totalMin}m`
  const totalHr = Math.floor(totalMin / 60)
  if (totalHr < 24) {
    const m = totalMin % 60
    return m === 0 ? `${totalHr}h` : `${totalHr}h ${m}m`
  }
  const days = Math.floor(totalHr / 24)
  const hr = totalHr % 24
  return hr === 0 ? `${days}d` : `${days}d ${hr}h`
}

/**
 * Format a millisecond duration as a short elapsed-time string for
 * run rows: "12.4s", "4m 06s", "1h 12m".
 */
export function formatDuration(ms: number): string {
  if (ms < 0) return '—'
  if (ms < 1_000) return `${ms}ms`
  const totalSec = ms / 1000
  if (totalSec < 60) return `${totalSec.toFixed(1)}s`
  const totalMin = Math.floor(totalSec / 60)
  const sec = Math.floor(totalSec - totalMin * 60)
  if (totalMin < 60) {
    return `${totalMin}m ${sec.toString().padStart(2, '0')}s`
  }
  const hr = Math.floor(totalMin / 60)
  const min = totalMin - hr * 60
  return `${hr}h ${min}m`
}

/**
 * Group schedules by provider, returning provider name → count.
 * Used by the Overview "Providers" card to derive an at-a-glance list
 * without a dedicated providers-health endpoint.
 */
export function schedulesByProvider(
  schedules: Schedule[],
): { provider: string; jobCount: number; activeCount: number }[] {
  const byProv = new Map<string, { active: number; total: number }>()
  for (const s of schedules) {
    const key = s.provider || 'unknown'
    const cur = byProv.get(key) ?? { active: 0, total: 0 }
    cur.total++
    if (s.enabled) cur.active++
    byProv.set(key, cur)
  }
  return [...byProv.entries()]
    .map(([provider, v]) => ({
      provider,
      jobCount: v.total,
      activeCount: v.active,
    }))
    .sort((a, b) => b.jobCount - a.jobCount)
}
