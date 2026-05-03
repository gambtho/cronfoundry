// web/src/lib/api.ts
import { ApiError } from './api-error'
import type {
  RepoConnection, Skill, Schedule, RunSummary, RunDetail, RunEvent, RunNotification, SecretMeta, Me, AuditEntry, UserDTO,
  SystemHealth, Alerts
} from './types'

function csrfToken(): string | undefined {
  const m = document.cookie.split('; ').find(c => c.startsWith('cf_csrf='))
  return m ? m.slice('cf_csrf='.length) : undefined
}

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? 'GET').toUpperCase()
  const headers = new Headers(init?.headers)
  if (!SAFE_METHODS.has(method)) {
    const t = csrfToken()
    if (t) headers.set('X-CSRF-Token', t)
  }
  const res = await fetch(path, { credentials: 'include', ...init, headers })
  if (res.status === 401) {
    window.location.href = '/oauth/login'
    return Promise.reject(new Error('unauthorized'))
  }
  if (res.status === 403) {
    // CSRF rejection (cookie missing / mismatch) → re-auth flow.
    // Origin-mismatch / role 403 are real authz errors and fall through.
    const body = await res.clone().json().catch(() => ({}))
    if (typeof body.error === 'string' && body.error.startsWith('csrf')) {
      window.location.href = '/oauth/login'
      return Promise.reject(new Error('csrf rejected; re-authenticating'))
    }
  }
  if (!res.ok) {
    const raw: unknown = await res.json().catch(() => null)
    const body: Record<string, unknown> =
      raw !== null && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
    const message =
      typeof body.error === 'string' && body.error ? body.error : res.statusText
    const code = typeof body.code === 'string' && body.code ? body.code : 'unknown'
    const extras: Record<string, unknown> = { ...body }
    delete extras.error
    delete extras.code
    throw new ApiError(message, res.status, code, extras)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

/**
 * ProposeJobScheduleInput is the JSON shape of `config.Schedule` from the
 * Go backend (matched against its json tags). Only `name`, `cron`, `provider`,
 * `model`, and `destinations` are practically required for a runnable
 * schedule; the rest are optional and omitted when zero.
 */
export interface ProposeJobScheduleInput {
  name: string
  cron: string
  timezone?: string
  overlap_policy?: string
  timeout_sec?: number
  provider: string
  model: string
  max_turns?: number
  copilot_prefix?: string
  destinations?: unknown[] // shaped per config.Destination on the server; v1 sends []
  writeback?: { enabled: boolean; path: string; mode: 'append' | 'replace' }
  env?: Record<string, { value?: string; secret_ref?: string }>
  mcp_env?: Record<string, Record<string, { value?: string; secret_ref?: string }>>
  auto_pause?: unknown
}

export interface ProposeJobRequest {
  skill_path: string
  schedule: ProposeJobScheduleInput
}

export interface ProposeJobResponse {
  pr_url: string
  pr_number: number
  branch: string
}

export const api = {
  me: () => apiFetch<Me>('/api/me'),

  repos: {
    list: () => apiFetch<RepoConnection[]>('/api/repos'),
    connect: (body: { install_id: number; owner: string; name: string; default_branch?: string }) =>
      apiFetch<RepoConnection>('/api/repos', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }),
    disconnect: (id: string) => apiFetch<void>(`/api/repos/${id}`, { method: 'DELETE' }),
  },

  skills: {
    list: () => apiFetch<Skill[]>('/api/skills'),
  },

  schedules: {
    list: () => apiFetch<Schedule[]>('/api/schedules'),
    pause: (id: string) => apiFetch<Schedule>(`/api/schedules/${id}/pause`, { method: 'POST' }),
    resume: (id: string) => apiFetch<Schedule>(`/api/schedules/${id}/resume`, { method: 'POST' }),
    runNow: (id: string) => apiFetch<{ run_id: string }>(`/api/schedules/${id}/run-now`, { method: 'POST' }),
    patchOverrides: (id: string, overrides: { cron?: string; timezone?: string; timeout_sec?: number; enabled?: boolean }) =>
      apiFetch<void>(`/api/schedules/${id}/overrides`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(overrides),
      }),
    clearOverrides: (id: string) =>
      apiFetch<void>(`/api/schedules/${id}/overrides`, { method: 'DELETE' }),
  },

  runs: {
    list: (params?: { limit?: number; schedule_id?: string }) => {
      const qs = new URLSearchParams()
      if (params?.limit) qs.set('limit', String(params.limit))
      if (params?.schedule_id) qs.set('schedule_id', params.schedule_id)
      return apiFetch<RunSummary[]>(`/api/runs?${qs}`)
    },
    get: (id: string) => apiFetch<RunDetail>(`/api/runs/${id}`),
    events: (id: string) => apiFetch<RunEvent[]>(`/api/runs/${id}/events`),
    notifications: (id: string) => apiFetch<RunNotification[]>(`/api/runs/${id}/notifications`),
    eventsStreamURL: (id: string) => `/api/runs/${id}/events/stream`,
  },

  secrets: {
    list: () => apiFetch<SecretMeta[]>('/api/secrets'),
    create: (name: string, value: string) =>
      apiFetch<{ name: string }>('/api/secrets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, value }),
      }),
    rotate: (name: string, value: string) =>
      apiFetch<{ name: string }>(`/api/secrets/${name}/rotate`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ value }),
      }),
    delete: (name: string) => apiFetch<void>(`/api/secrets/${name}`, { method: 'DELETE' }),
  },

  audit: {
    list: (params?: { limit?: number; offset?: number }) => {
      const qs = new URLSearchParams()
      if (params?.limit) qs.set('limit', String(params.limit))
      if (params?.offset) qs.set('offset', String(params.offset))
      const q = qs.toString()
      return apiFetch<AuditEntry[]>(q ? `/api/audit?${q}` : '/api/audit')
    },
  },

  system: {
    health: () => apiFetch<SystemHealth>('/api/system/health'),
  },

  alerts: {
    list: () => apiFetch<Alerts>('/api/alerts'),
  },

  skillRepo: {
    proposeJob: (req: ProposeJobRequest) =>
      apiFetch<ProposeJobResponse>('/api/skill-repo/jobs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      }),
  },

  users: {
    list: () => apiFetch<UserDTO[]>('/api/users'),
    create: (login: string, role: 'admin' | 'viewer') =>
      apiFetch<UserDTO>('/api/users', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ login, role }),
      }),
    updateRole: (login: string, role: 'admin' | 'viewer') =>
      apiFetch<void>(`/api/users/${encodeURIComponent(login)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ role }),
      }),
    delete: (login: string) =>
      apiFetch<void>(`/api/users/${encodeURIComponent(login)}`, { method: 'DELETE' }),
  },
}
