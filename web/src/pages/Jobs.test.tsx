import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, cleanup, waitFor, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Jobs from './Jobs'
import type { RunSummary, Schedule } from '../lib/types'

// Stub the API surface — pages render under a fresh QueryClient and
// react-query takes care of caching, so we just intercept the
// underlying fetch path.
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
      get: vi.fn(),
      events: vi.fn(),
      eventsStreamURL: vi.fn(),
    },
    me: vi.fn(),
    repos: { list: vi.fn() },
    secrets: { list: vi.fn() },
    audit: { list: vi.fn() },
    users: { list: vi.fn() },
    skills: { list: vi.fn() },
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
): RunSummary => ({
  id: `r-${Math.random()}`,
  status,
  fire_reason: 'schedule',
  actor: null,
  started_at: new Date().toISOString(),
  finished_at: null,
  duration_ms: 4000,
  error_kind: status === 'failed' ? 'Exit 1' : null,
  error_msg: status === 'failed' ? 'pg_dump connection refused' : null,
  created_at: new Date().toISOString(),
  schedule_name,
  skill_path: 'p',
  owner: 'o',
  repo_name: 'r',
})

function withProviders(ui: React.ReactNode) {
  // A fresh QueryClient per test isolates cache between cases.
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/jobs']}>
        <Routes>
          <Route path="/jobs" element={ui} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

describe('Jobs page', () => {
  it('renders schedules in a table', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([
      sched({ id: 's1', name: 'nightly-backup' }),
      sched({ id: 's2', name: 'stripe-reconcile' }),
    ])
    vi.mocked(api.runs.list).mockResolvedValue([])

    const { findAllByRole, container } = render(withProviders(<Jobs />))

    // Wait for the data rows to appear, then check the link text.
    await findAllByRole('row')
    await waitFor(() => {
      const links = container.querySelectorAll('tbody a')
      const names = [...links].map((a) => a.textContent)
      expect(names).toContain('nightly-backup')
      expect(names).toContain('stripe-reconcile')
    })
  })

  it('shows the failing chip count and pins failing rows when sorted alert-first', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([
      sched({ id: 's1', name: 'good-job' }),
      sched({ id: 's2', name: 'bad-job' }),
    ])
    vi.mocked(api.runs.list).mockResolvedValue([
      run('bad-job', 'failed'),
      run('good-job', 'succeeded'),
    ])

    const { container } = render(withProviders(<Jobs />))

    // Wait for the failing chip to update its count from the data.
    await waitFor(() => {
      const chips = [...container.querySelectorAll('button[aria-pressed]')]
      const failing = chips.find((c) => c.textContent?.startsWith('Failing'))
      expect(failing?.textContent).toMatch(/Failing\s*1/)
    })

    // First data row should be the failing job (alert-first sort).
    await waitFor(() => {
      const rows = container.querySelectorAll('tbody tr')
      expect(rows[0]?.textContent).toContain('bad-job')
    })
  })

  it('renders an empty state when no jobs', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([])
    vi.mocked(api.runs.list).mockResolvedValue([])

    const { findByText } = render(withProviders(<Jobs />))
    expect(await findByText(/No jobs yet/i)).toBeInTheDocument()
  })
})

describe('Jobs page nav buttons', () => {
  it('+ Add job navigates to /jobs/new', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([])
    vi.mocked(api.runs.list).mockResolvedValue([])
    render(
      <MemoryRouter initialEntries={['/jobs']}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
          <Routes>
            <Route path="/jobs" element={<Jobs />} />
            <Route path="/jobs/new" element={<div data-testid="new-page" />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    )
    fireEvent.click(await screen.findByText('+ Add job'))
    expect(await screen.findByTestId('new-page')).toBeInTheDocument()
  })

  it('+ Import job navigates to /jobs/import', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([])
    vi.mocked(api.runs.list).mockResolvedValue([])
    render(
      <MemoryRouter initialEntries={['/jobs']}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
          <Routes>
            <Route path="/jobs" element={<Jobs />} />
            <Route path="/jobs/import" element={<div data-testid="import-page" />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    )
    fireEvent.click(await screen.findByText('+ Import job'))
    expect(await screen.findByTestId('import-page')).toBeInTheDocument()
  })

  it('pressing N navigates to /jobs/new', async () => {
    vi.mocked(api.schedules.list).mockResolvedValue([])
    vi.mocked(api.runs.list).mockResolvedValue([])
    render(
      <MemoryRouter initialEntries={['/jobs']}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
          <Routes>
            <Route path="/jobs" element={<Jobs />} />
            <Route path="/jobs/new" element={<div data-testid="new-page" />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    )
    await screen.findByText('+ Add job')
    fireEvent.keyDown(window, { key: 'n' })
    expect(await screen.findByTestId('new-page')).toBeInTheDocument()
  })

  it('Run-now navigates to /runs/<id> on success', async () => {
    vi.mocked(api.schedules.runNow as any).mockResolvedValue({ run_id: 'run-xyz' })
    vi.mocked(api.schedules.list).mockResolvedValue([sched()])
    vi.mocked(api.runs.list).mockResolvedValue([])
    render(
      <MemoryRouter initialEntries={['/jobs']}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
          <Routes>
            <Route path="/jobs" element={<Jobs />} />
            <Route path="/runs/:id" element={<div data-testid="run-page" />} />
          </Routes>
        </QueryClientProvider>
      </MemoryRouter>,
    )
    const btn = await screen.findByLabelText(/run nightly-backup now/i)
    fireEvent.click(btn)
    expect(await screen.findByTestId('run-page')).toBeInTheDocument()
  })
})
