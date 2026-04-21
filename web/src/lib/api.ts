// web/src/lib/api.ts
import type {
  RepoConnection, Skill, Schedule, RunSummary, RunDetail, RunEvent, SecretMeta, Me, AuditEntry, UserDTO
} from './types'

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: 'include', ...init })
  if (res.status === 401) {
    window.location.href = '/oauth/login'
    return Promise.reject(new Error('unauthorized'))
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error((body as { error?: string }).error ?? res.statusText)
  }
  if (res.status === 204) return undefined as T
  return res.json()
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
    runNow: (id: string) => apiFetch<void>(`/api/schedules/${id}/run-now`, { method: 'POST' }),
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
