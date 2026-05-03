import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Repos from './Repos'
import Secrets from './Secrets'
import Audit from './Audit'
import type {
  AuditEntry,
  RepoConnection,
  SecretMeta,
} from '../lib/types'

vi.mock('../lib/api', () => ({
  api: {
    repos: { list: vi.fn(), connect: vi.fn(), disconnect: vi.fn() },
    secrets: { list: vi.fn(), create: vi.fn(), rotate: vi.fn(), delete: vi.fn() },
    audit: { list: vi.fn() },
    // Topbar.Search reads schedules to power the dropdown; even on
    // settings pages where the search isn't the focus of the test
    // we still need a stub to keep it from throwing.
    schedules: { list: vi.fn().mockResolvedValue([]) },
  },
}))

import { api } from '../lib/api'

afterEach(cleanup)

function withProviders(ui: React.ReactNode, route = '/') {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return (
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>
        <Routes>
          <Route path="/" element={ui} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

const repo = (over: Partial<RepoConnection> = {}): RepoConnection => ({
  id: 'r1',
  org_id: 'o1',
  github_app_install_id: 1,
  owner: 'octocat',
  name: 'hello',
  default_branch: 'main',
  sync_interval_sec: 60,
  last_synced_at: new Date().toISOString(),
  last_sync_error: null,
  created_at: new Date().toISOString(),
  ...over,
})

const secret = (over: Partial<SecretMeta> = {}): SecretMeta => ({
  name: 'API_KEY',
  version: 1,
  last_updated: new Date().toISOString(),
  // last_used is optional on SecretMeta but Secrets.tsx renders it
  // either way; default to undefined here so the factory mirrors
  // the wire shape and `over` can override.
  last_used: undefined,
  ...over,
})

const audit = (over: Partial<AuditEntry> = {}): AuditEntry => ({
  id: 1,
  ts: new Date().toISOString(),
  actor: 'tng',
  action: 'schedule.run_now',
  target_kind: 'schedule',
  target_id: 's1234567890',
  detail: null,
  ...over,
})

describe('Repos page', () => {
  it('renders repo rows', async () => {
    vi.mocked(api.repos.list).mockResolvedValue([
      repo({ owner: 'octocat', name: 'hello' }),
      repo({ id: 'r2', owner: 'github', name: 'docs' }),
    ])
    const { findByText } = render(withProviders(<Repos />))
    expect(await findByText('octocat/hello')).toBeInTheDocument()
    expect(await findByText('github/docs')).toBeInTheDocument()
  })

  it('flags repos with sync errors', async () => {
    vi.mocked(api.repos.list).mockResolvedValue([
      repo({ last_sync_error: 'oauth token expired' }),
    ])
    const { findByText } = render(withProviders(<Repos />))
    expect(await findByText('Sync error')).toBeInTheDocument()
    expect(await findByText('oauth token expired')).toBeInTheDocument()
  })

  it('shows empty state when no repos', async () => {
    vi.mocked(api.repos.list).mockResolvedValue([])
    const { findByText } = render(withProviders(<Repos />))
    expect(await findByText(/No repos connected yet/i)).toBeInTheDocument()
  })
})

describe('Secrets page', () => {
  it('renders secrets in a table', async () => {
    vi.mocked(api.secrets.list).mockResolvedValue([
      secret({ name: 'STRIPE_KEY' }),
      secret({ name: 'DB_PASSWORD' }),
    ])
    const { findByText } = render(withProviders(<Secrets />))
    expect(await findByText('STRIPE_KEY')).toBeInTheDocument()
    expect(await findByText('DB_PASSWORD')).toBeInTheDocument()
  })

  it('opens the create modal when "Add secret" is clicked', async () => {
    vi.mocked(api.secrets.list).mockResolvedValue([])
    const { findByText, getByRole } = render(withProviders(<Secrets />))
    fireEvent.click(await findByText('+ Add secret'))
    // Modal title shows.
    expect(getByRole('dialog', { name: /Add secret/i })).toBeInTheDocument()
  })
})

describe('Audit page', () => {
  it('groups by action prefix when filter is set', async () => {
    vi.mocked(api.audit.list).mockResolvedValue([
      audit({ id: 1, action: 'secret.create' }),
      audit({ id: 2, action: 'schedule.run_now' }),
    ])
    const { findByText, queryByText, getByLabelText } = render(
      withProviders(<Audit />),
    )

    expect(await findByText('secret.create')).toBeInTheDocument()
    expect(await findByText('schedule.run_now')).toBeInTheDocument()

    const groupSelect = getByLabelText(/group/i) as HTMLSelectElement
    fireEvent.change(groupSelect, { target: { value: 'secret' } })

    expect(await findByText('secret.create')).toBeInTheDocument()
    expect(queryByText('schedule.run_now')).toBeNull()
  })
})
