import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import RunDetail from './RunDetail'
import type { RunDetail as RunDetailType, RunNotification, Schedule } from '../lib/types'

vi.mock('../lib/api', () => ({
  api: {
    schedules: {
      list: vi.fn(),
      runNow: vi.fn(),
    },
    runs: {
      list: vi.fn(),
      get: vi.fn(),
      events: vi.fn(),
      notifications: vi.fn(),
      eventsStreamURL: vi.fn(),
    },
  },
}))

import { api } from '../lib/api'

afterEach(cleanup)

const sched = (over: Partial<Schedule> = {}): Schedule => ({
  id: 's1',
  skill_id: 'sk',
  name: 'nightly-backup',
  cron: '0 2 * * *',
  timezone: 'UTC',
  overlap_policy: 'skip',
  timeout_sec: 60,
  enabled: true,
  provider: 'aws-prod',
  model: 'sonnet',
  next_fire_at: null,
  auto_pause_after: null,
  auto_paused_at: null,
  auto_pause_reason: null,
  last_enabled_at: '2025-01-01T00:00:00Z',
  skill_path: 'p',
  skill_name: 'nightly-backup',
  owner: 'o',
  repo_name: 'r',
  max_turns: null,
  mcp_servers: [],
  has_ui_overrides: false,
  ...over,
})

const detail = (over: Partial<RunDetailType> = {}): RunDetailType => ({
  id: 'r-1234567890abcdef',
  status: 'succeeded',
  fire_reason: 'schedule',
  actor: null,
  started_at: '2025-05-03T02:00:00Z',
  finished_at: '2025-05-03T02:04:00Z',
  duration_ms: 240_000,
  error_kind: null,
  error_msg: null,
  created_at: '2025-05-03T02:00:00Z',
  schedule_name: 'nightly-backup',
  skill_path: 'p',
  owner: 'o',
  repo_name: 'r',
  tokens_in: 1000,
  tokens_out: 500,
  cost_cents: 25,
  writeback_commit_sha: null,
  ...over,
})

function withProviders(ui: React.ReactNode, runId = 'r-1234567890abcdef') {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/runs/${runId}`]}>
        <Routes>
          <Route path="/runs/:id" element={ui} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('RunDetail page', () => {
  beforeEach(() => {
    vi.mocked(api.runs.notifications).mockResolvedValue([])
  })

  it('renders meta strip and links back to the parent job', async () => {
    vi.mocked(api.runs.get).mockResolvedValue(detail({ status: 'succeeded' }))
    vi.mocked(api.runs.list).mockResolvedValue([])
    vi.mocked(api.runs.events).mockResolvedValue([])
    vi.mocked(api.schedules.list).mockResolvedValue([sched()])

    const { findByText, findAllByText, container } = render(
      withProviders(<RunDetail />),
    )

    // Meta strip cells.
    await findByText('Status')
    await findByText('Duration')
    // 240_000 ms → "4m 00s"
    expect(await findByText('4m 00s')).toBeInTheDocument()
    // Provider derived from the parent schedule lookup.
    expect(await findByText('aws-prod')).toBeInTheDocument()

    // Crumb back to the parent job — the schedule-name appears in
    // both the breadcrumb and the subtitle, hence findAllByText.
    const refs = await findAllByText('nightly-backup')
    expect(refs.length).toBeGreaterThan(0)
    // At least one anchor points to /jobs/s1.
    expect(
      [...container.querySelectorAll('a')].some(
        (a) => a.getAttribute('href') === '/jobs/s1',
      ),
    ).toBe(true)
  })

  it('shows the failure card with the error message', async () => {
    vi.mocked(api.runs.get).mockResolvedValue(
      detail({
        status: 'failed',
        error_kind: 'Exit 1',
        error_msg: 'pg_dump: connection refused',
      }),
    )
    vi.mocked(api.runs.list).mockResolvedValue([])
    vi.mocked(api.runs.events).mockResolvedValue([])
    vi.mocked(api.schedules.list).mockResolvedValue([sched()])

    const { findByText } = render(withProviders(<RunDetail />))

    expect(
      await findByText(/pg_dump: connection refused/i),
    ).toBeInTheDocument()
    // Error eyebrow.
    expect(await findByText(/Failed · Exit 1/i)).toBeInTheDocument()
  })

  it('shows a not-found error when the run is missing', async () => {
    vi.mocked(api.runs.get).mockRejectedValue(new Error('404'))
    vi.mocked(api.runs.list).mockResolvedValue([])
    vi.mocked(api.runs.events).mockResolvedValue([])
    vi.mocked(api.schedules.list).mockResolvedValue([])

    const { findByText } = render(withProviders(<RunDetail />, 'missing'))

    expect(await findByText(/Run not found/i)).toBeInTheDocument()
  })

  it('shows the notifications card with rows for a terminal run', async () => {
    const notifs: RunNotification[] = [
      {
        id: 1,
        run_id: 'r-1234567890abcdef',
        kind: 'slack',
        target: '#alerts',
        status: 'sent',
        reason: null,
        created_at: '2025-05-03T02:04:01Z',
      },
      {
        id: 2,
        run_id: 'r-1234567890abcdef',
        kind: 'email',
        target: 'oncall@example.com',
        status: 'failed',
        reason: 'smtp timeout',
        created_at: '2025-05-03T02:04:02Z',
      },
    ]
    vi.mocked(api.runs.get).mockResolvedValue(detail({ status: 'succeeded' }))
    vi.mocked(api.runs.list).mockResolvedValue([])
    vi.mocked(api.runs.events).mockResolvedValue([])
    vi.mocked(api.runs.notifications).mockResolvedValue(notifs)
    vi.mocked(api.schedules.list).mockResolvedValue([sched()])

    const { findByText } = render(withProviders(<RunDetail />))

    expect(await findByText('Notifications sent')).toBeInTheDocument()
    expect(await findByText('slack')).toBeInTheDocument()
    expect(await findByText('#alerts')).toBeInTheDocument()
    expect(await findByText('email')).toBeInTheDocument()
    expect(await findByText('oncall@example.com')).toBeInTheDocument()
    expect(await findByText('smtp timeout')).toBeInTheDocument()
  })

  it('shows empty-state for terminal run with no notifications', async () => {
    vi.mocked(api.runs.get).mockResolvedValue(detail({ status: 'succeeded' }))
    vi.mocked(api.runs.list).mockResolvedValue([])
    vi.mocked(api.runs.events).mockResolvedValue([])
    vi.mocked(api.runs.notifications).mockResolvedValue([])
    vi.mocked(api.schedules.list).mockResolvedValue([sched()])

    const { findByText } = render(withProviders(<RunDetail />))

    expect(await findByText('Notifications sent')).toBeInTheDocument()
    expect(
      await findByText(/No destinations configured for this run\./i),
    ).toBeInTheDocument()
  })

  it('hides the notifications card while the run is non-terminal', async () => {
    // jsdom has no EventSource; LogTail opens one for non-terminal runs.
    const OriginalES = (globalThis as { EventSource?: unknown }).EventSource
    ;(globalThis as { EventSource: unknown }).EventSource = class {
      close() {}
      addEventListener() {}
      onopen: ((e: unknown) => void) | null = null
      onmessage: ((e: unknown) => void) | null = null
      onerror: ((e: unknown) => void) | null = null
    }
    try {
      vi.mocked(api.runs.get).mockResolvedValue(detail({ status: 'running' }))
      vi.mocked(api.runs.list).mockResolvedValue([])
      vi.mocked(api.runs.events).mockResolvedValue([])
      vi.mocked(api.runs.notifications).mockResolvedValue([])
      vi.mocked(api.schedules.list).mockResolvedValue([sched()])

      const { findByText, queryByText } = render(withProviders(<RunDetail />))

      // Wait for the page to settle.
      await findByText('Status')
      expect(queryByText('Notifications sent')).toBeNull()
    } finally {
      if (OriginalES === undefined) {
        delete (globalThis as { EventSource?: unknown }).EventSource
      } else {
        ;(globalThis as { EventSource: unknown }).EventSource = OriginalES
      }
    }
  })
})
