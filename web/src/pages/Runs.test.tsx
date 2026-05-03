import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Runs from './Runs'
import type { RunSummary } from '../lib/types'

vi.mock('../lib/api', () => ({
  api: {
    runs: { list: vi.fn() },
    schedules: { list: vi.fn() },
  },
}))

import { api } from '../lib/api'

afterEach(cleanup)

const run = (
  schedule_name: string,
  status: RunSummary['status'],
  overrides: Partial<RunSummary> = {},
): RunSummary => ({
  id: `r-${Math.random()}`,
  status,
  fire_reason: 'schedule',
  actor: null,
  started_at: new Date().toISOString(),
  finished_at: null,
  duration_ms: 4000,
  error_kind: status === 'failed' ? 'Exit 1' : null,
  error_msg: null,
  created_at: new Date().toISOString(),
  schedule_name,
  skill_path: 'p',
  owner: 'o',
  repo_name: 'r',
  ...overrides,
})

function withProviders(ui: React.ReactNode, initialEntry = '/runs') {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route path="/runs" element={ui} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('Runs page', () => {
  it('renders runs in a table', async () => {
    vi.mocked(api.runs.list).mockResolvedValue([
      run('nightly', 'succeeded'),
      run('weekly', 'failed'),
    ])
    // Topbar.Search reads schedules; default to empty so the query
    // doesn't return undefined.
    vi.mocked(api.schedules.list).mockResolvedValue([])

    const { findByText } = render(withProviders(<Runs />))

    expect(await findByText('nightly')).toBeInTheDocument()
    expect(await findByText('weekly')).toBeInTheDocument()
  })

  it('shows error UI when fetch fails', async () => {
    vi.mocked(api.runs.list).mockRejectedValue(new Error('boom'))
    // Topbar.Search reads schedules — give it an empty list so the
    // query doesn't return undefined and trigger a noisy warning.
    vi.mocked(api.schedules.list).mockResolvedValue([])

    const { findByText } = render(withProviders(<Runs />))

    expect(await findByText(/Could not load runs/i)).toBeInTheDocument()
  })

  it('shows the schedule_id filter chip when querystring is set', async () => {
    vi.mocked(api.runs.list).mockResolvedValue([])
    vi.mocked(api.schedules.list).mockResolvedValue([
      {
        id: 'sched-42',
        skill_id: 'sk',
        name: 'nightly',
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
        owner: 'o',
        repo_name: 'r',
        max_turns: null,
        mcp_servers: [],
        has_ui_overrides: false,
      },
    ])

    const { findByText } = render(
      withProviders(<Runs />, '/runs?schedule_id=sched-42'),
    )

    expect(await findByText(/schedule = nightly/i)).toBeInTheDocument()
  })
})
