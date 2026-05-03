import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Overview from './Overview'
import type { RunSummary, Schedule } from '../lib/types'

vi.mock('../lib/api', () => ({
  api: {
    schedules: {
      list: vi.fn(),
      runNow: vi.fn(),
      pause: vi.fn(),
      resume: vi.fn(),
    },
    runs: {
      list: vi.fn(),
      get: vi.fn(),
    },
    system: {
      health: vi.fn(),
    },
    alerts: {
      list: vi.fn(),
    },
    me: vi.fn(),
  },
}))

import { api } from '../lib/api'

afterEach(cleanup)

const defaultHealth = {
  scheduler: { status: 'healthy' as const, last_tick_at: null },
  queue_depth: 0,
  workers: 1,
  last_sync_at: null,
}
const defaultAlerts = {
  quiet_jobs: [],
  recently_paused: [],
  expiring_secrets: [] as never[],
  drift: [] as never[],
}

const sched = (over: Partial<Schedule> = {}): Schedule => ({
  id: 's1',
  skill_id: 'sk',
  name: 'nightly-backup',
  cron: '0 2 * * *',
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
  skill_path: 'jobs/x.yaml',
  skill_name: 'x',
  owner: 'org',
  repo_name: 'pipe',
  max_turns: null,
  mcp_servers: [],
  has_ui_overrides: false,
  ...over,
})

const run = (
  schedule_name: string,
  status: RunSummary['status'],
): RunSummary => ({
  id: `r-${Math.random()}`,
  status,
  fire_reason: 'schedule',
  actor: null,
  started_at: new Date().toISOString(),
  finished_at: null,
  duration_ms: 4000,
  error_kind: status === 'failed' ? 'Exit 1' : null,
  error_msg: status === 'failed' ? 'connection refused' : null,
  created_at: new Date().toISOString(),
  schedule_name,
  skill_path: 'p',
  owner: 'o',
  repo_name: 'r',
})

function withProviders(ui: React.ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/overview']}>
        <Routes>
          <Route path="/overview" element={ui} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('Overview page', () => {
  it('renders failure triage when a schedule is failing', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([
      sched({ id: 's1', name: 'broken-job' }),
    ])
    vi.mocked(api.runs.list).mockResolvedValue([run('broken-job', 'failed')])
    vi.mocked(api.system.health).mockResolvedValue(defaultHealth)
    vi.mocked(api.alerts.list).mockResolvedValue(defaultAlerts)

    const { findByText } = render(withProviders(<Overview />))

    expect(await findByText(/Failing now/i)).toBeInTheDocument()
    expect(await findByText('broken-job')).toBeInTheDocument()
    expect(await findByText('connection refused')).toBeInTheDocument()
  })

  it('shows the healthy eyebrow when nothing is failing', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([
      sched({ id: 's1', name: 'happy-job' }),
    ])
    vi.mocked(api.runs.list).mockResolvedValue([run('happy-job', 'succeeded')])
    vi.mocked(api.system.health).mockResolvedValue(defaultHealth)
    vi.mocked(api.alerts.list).mockResolvedValue(defaultAlerts)

    const { findByText, queryByText } = render(withProviders(<Overview />))

    expect(await findByText(/all healthy/i)).toBeInTheDocument()
    // No "Failing now" card.
    expect(queryByText(/Failing now/i)).toBeNull()
  })
})
