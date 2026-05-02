import { describe, it, expect } from 'vitest'
import {
  activityByHour,
  failingSchedules,
  formatCountdown,
  formatDuration,
  lastRunByScheduleName,
  schedulesByProvider,
  upcomingRuns,
} from './derived'
import type { RunSummary, Schedule } from './types'

// Test fixtures kept terse — only the fields the helpers read are
// populated; the rest are cast to keep the types happy.

function run(
  schedule_name: string,
  status: RunSummary['status'],
  started_at: string | null = null,
): RunSummary {
  return {
    id: `r-${Math.random()}`,
    status,
    fire_reason: 'schedule',
    actor: null,
    started_at,
    finished_at: null,
    duration_ms: null,
    error_kind: null,
    error_msg: null,
    created_at: started_at ?? new Date().toISOString(),
    schedule_name,
    skill_path: 'x',
    owner: 'o',
    repo_name: 'r',
  }
}

function schedule(
  name: string,
  overrides: Partial<Schedule> = {},
): Schedule {
  return {
    id: `s-${name}`,
    skill_id: 'sk',
    name,
    cron: '0 * * * *',
    timezone: 'UTC',
    overlap_policy: 'skip',
    timeout_sec: 60,
    enabled: true,
    provider: 'aws',
    model: 'sonnet',
    next_fire_at: null,
    auto_pause_after: null,
    auto_paused_at: null,
    auto_pause_reason: null,
    last_enabled_at: '2025-01-01T00:00:00Z',
    skill_path: 'p',
    skill_name: name,
    owner: 'o',
    repo_name: 'r',
    max_turns: null,
    mcp_servers: [],
    has_ui_overrides: false,
    ...overrides,
  }
}

describe('lastRunByScheduleName', () => {
  it('keeps the first occurrence per schedule (newest-first input)', () => {
    const m = lastRunByScheduleName([
      run('a', 'failed'),
      run('a', 'succeeded'),
      run('b', 'succeeded'),
    ])
    expect(m.get('a')!.status).toBe('failed')
    expect(m.get('b')!.status).toBe('succeeded')
  })

  it('returns an empty map for empty input', () => {
    expect(lastRunByScheduleName([]).size).toBe(0)
  })
})

describe('failingSchedules', () => {
  it('returns only schedules whose latest run is failed/partial', () => {
    const result = failingSchedules(
      [schedule('a'), schedule('b'), schedule('c')],
      [
        run('a', 'failed'),
        run('a', 'failed'),
        run('a', 'succeeded'),
        run('b', 'succeeded'),
        run('c', 'partial_failure'),
        run('c', 'succeeded'),
      ],
    )
    expect(result.map((r) => r.schedule.name).sort()).toEqual(['a', 'c'])
  })

  it('counts the consecutive failure streak from the head', () => {
    const result = failingSchedules(
      [schedule('a')],
      [
        run('a', 'failed'),
        run('a', 'partial_failure'),
        run('a', 'failed'),
        run('a', 'succeeded'),
        run('a', 'failed'),
      ],
    )
    expect(result).toHaveLength(1)
    expect(result[0].streak).toBe(3)
  })

  it('sorts most-broken first', () => {
    const result = failingSchedules(
      [schedule('low'), schedule('high')],
      [
        run('low', 'failed'),
        run('low', 'succeeded'),
        run('high', 'failed'),
        run('high', 'failed'),
        run('high', 'failed'),
      ],
    )
    expect(result[0].schedule.name).toBe('high')
  })

  it('skips schedules with no runs', () => {
    expect(failingSchedules([schedule('a')], [])).toEqual([])
  })
})

describe('upcomingRuns', () => {
  it('drops schedules without next_fire_at', () => {
    const out = upcomingRuns([
      schedule('a', { next_fire_at: null }),
      schedule('b', { next_fire_at: '2099-01-01T00:00:00Z' }),
    ])
    expect(out.map((u) => u.schedule.name)).toEqual(['b'])
  })

  it('sorts soonest first', () => {
    const now = new Date('2025-05-03T12:00:00Z')
    const out = upcomingRuns(
      [
        schedule('later', { next_fire_at: '2025-05-04T12:00:00Z' }),
        schedule('soon', { next_fire_at: '2025-05-03T13:00:00Z' }),
      ],
      now,
    )
    expect(out.map((u) => u.schedule.name)).toEqual(['soon', 'later'])
    expect(out[0].msUntil).toBe(60 * 60 * 1000)
  })

  it('clamps past-due fire times to zero', () => {
    const now = new Date('2025-05-03T12:00:00Z')
    const out = upcomingRuns(
      [schedule('a', { next_fire_at: '2025-05-03T11:30:00Z' })],
      now,
    )
    expect(out[0].msUntil).toBe(0)
  })
})

describe('activityByHour', () => {
  it('produces 24 buckets in chronological order', () => {
    const out = activityByHour([], 24, new Date('2025-05-03T12:30:00Z'))
    expect(out).toHaveLength(24)
  })

  it('classifies runs into the correct bucket and tally', () => {
    const now = new Date('2025-05-03T12:30:00Z')
    // current-hour boundary is 12:00; bucket index 23 represents 12:00..13:00.
    const out = activityByHour(
      [
        run('a', 'succeeded', '2025-05-03T12:15:00Z'),
        run('b', 'failed', '2025-05-03T12:20:00Z'),
        run('c', 'succeeded', '2025-05-03T11:30:00Z'), // bucket 22
        run('d', 'partial_failure', '2025-05-03T11:45:00Z'), // bucket 22
      ],
      24,
      now,
    )
    expect(out[23].succeeded).toBe(1)
    expect(out[23].failed).toBe(1)
    expect(out[23].total).toBe(2)
    expect(out[22].succeeded).toBe(1)
    expect(out[22].failed).toBe(1) // partial_failure rolls into failed
  })

  it('drops runs outside the window', () => {
    const now = new Date('2025-05-03T12:30:00Z')
    const out = activityByHour(
      [run('a', 'succeeded', '2025-05-01T00:00:00Z')],
      24,
      now,
    )
    expect(out.every((b) => b.total === 0)).toBe(true)
  })

  it('skips runs without started_at', () => {
    const out = activityByHour(
      [run('a', 'succeeded', null)],
      24,
      new Date('2025-05-03T12:30:00Z'),
    )
    expect(out.every((b) => b.total === 0)).toBe(true)
  })
})

describe('formatCountdown', () => {
  it.each([
    [10_000, 'now'],
    [60_000, '1m'],
    [60 * 60 * 1000, '1h'],
    [60 * 60 * 1000 + 12 * 60_000, '1h 12m'],
    [24 * 60 * 60 * 1000, '1d'],
    [(24 * 7 + 3) * 60 * 60 * 1000, '7d 3h'],
  ])('formats %i ms as %s', (ms, expected) => {
    expect(formatCountdown(ms)).toBe(expected)
  })
})

describe('formatDuration', () => {
  it.each([
    [-1, '—'],
    [120, '120ms'],
    [12_400, '12.4s'],
    [4 * 60_000 + 6_000, '4m 06s'],
    [60 * 60_000 + 12 * 60_000, '1h 12m'],
  ])('formats %i ms as %s', (ms, expected) => {
    expect(formatDuration(ms)).toBe(expected)
  })
})

describe('schedulesByProvider', () => {
  it('groups by provider, counts active vs total, sorts by total desc', () => {
    const out = schedulesByProvider([
      schedule('a', { provider: 'aws', enabled: true }),
      schedule('b', { provider: 'aws', enabled: false }),
      schedule('c', { provider: 'aws', enabled: true }),
      schedule('d', { provider: 'gcp', enabled: true }),
    ])
    expect(out).toEqual([
      { provider: 'aws', jobCount: 3, activeCount: 2 },
      { provider: 'gcp', jobCount: 1, activeCount: 1 },
    ])
  })

  it('treats empty provider as "unknown"', () => {
    const out = schedulesByProvider([schedule('a', { provider: '' })])
    expect(out[0].provider).toBe('unknown')
  })
})
