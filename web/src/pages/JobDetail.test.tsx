import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import JobDetail from './JobDetail'
import type { RunSummary, Schedule } from '../lib/types'

vi.mock('../lib/api', () => ({
  api: {
    schedules: {
      list: vi.fn(),
      runNow: vi.fn(),
      pause: vi.fn(),
      resume: vi.fn(),
      patchOverrides: vi.fn(),
      clearOverrides: vi.fn(),
    },
    runs: {
      list: vi.fn(),
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
  provider: 'aws',
  model: 'sonnet',
  next_fire_at: '2099-01-01T00:00:00Z',
  auto_pause_after: null,
  auto_paused_at: null,
  auto_pause_reason: null,
  last_enabled_at: '2025-01-01T00:00:00Z',
  skill_path: 'jobs/nightly-backup.yaml',
  skill_name: 'nightly-backup',
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
  overrides: Partial<RunSummary> = {},
): RunSummary => ({
  id: 'r-1234567890abcdef',
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
  ...overrides,
})

function withProviders(ui: React.ReactNode, jobId = 's1') {
  // We mount JobDetail behind a route that supplies the :id param,
  // matching how main.tsx wires it up in production.
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/jobs/${jobId}`]}>
        <Routes>
          <Route path="/jobs/:id" element={ui} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('JobDetail page', () => {
  it('renders the job header and schedule strip', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([
      sched({ id: 's1', name: 'nightly-backup', cron: '0 2 * * *' }),
    ])
    vi.mocked(api.runs.list).mockResolvedValue([])

    const { findByRole, findByText } = render(withProviders(<JobDetail />))

    // Title.
    const heading = await findByRole('heading', { level: 1 })
    expect(heading).toHaveTextContent('nightly-backup')
    // Cron in the schedule strip — humanizeCron renders it as
    // "every day at 02:00".
    expect(await findByText(/every day at 02:00/i)).toBeInTheDocument()
  })

  it('shows the failure banner when the most recent run failed', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([sched({ id: 's1' })])
    vi.mocked(api.runs.list).mockResolvedValue([
      run('nightly-backup', 'failed'),
    ])

    const { findByText, findAllByText } = render(withProviders(<JobDetail />))

    expect(
      await findByText(/Run exited with Exit 1/i),
    ).toBeInTheDocument()
    // The error message renders in both the failure banner and the
    // "Last run" preview card, so accept multiple matches.
    const matches = await findAllByText(/connection refused/i)
    expect(matches.length).toBeGreaterThan(0)
  })

  it('shows an empty state when no runs have happened', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([sched({ id: 's1' })])
    vi.mocked(api.runs.list).mockResolvedValue([])

    const { findByText } = render(withProviders(<JobDetail />))

    // The body copy is more specific than the header-meta copy
    // ("no runs yet" appears in both); match the actionable line.
    expect(
      await findByText(/Hit ▶ Run now or wait for the schedule/i),
    ).toBeInTheDocument()
  })

  it('renders a not-found message for an unknown id', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([sched({ id: 's1' })])
    vi.mocked(api.runs.list).mockResolvedValue([])

    const { findByText } = render(withProviders(<JobDetail />, 'no-such'))

    expect(await findByText(/Job not found/i)).toBeInTheDocument()
  })

  it('shows an error UI when schedules fail to load', async () => {
    vi.mocked(api.schedules.list).mockRejectedValue(new Error('boom'))
    vi.mocked(api.runs.list).mockResolvedValue([])

    const { findByText } = render(withProviders(<JobDetail />))

    expect(await findByText(/Could not load job/i)).toBeInTheDocument()
  })
})
