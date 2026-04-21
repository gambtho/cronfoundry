// web/src/lib/types.ts

export interface RepoConnection {
  id: string
  org_id: string
  github_app_install_id: number
  owner: string
  name: string
  default_branch: string
  sync_interval_sec: number
  last_synced_at: string | null
  last_sync_error: string | null
  created_at: string
}

export interface Skill {
  id: string
  repo_id: string
  path: string
  name: string
  current_sha: string
  updated_at: string
  owner: string
  repo_name: string
}

export interface Schedule {
  id: string
  skill_id: string
  name: string
  cron: string
  timezone: string
  overlap_policy: string
  timeout_sec: number
  enabled: boolean
  provider: string
  model: string
  next_fire_at: string | null
  skill_path: string
  skill_name: string
  owner: string
  repo_name: string
}

export type RunStatus = 'pending' | 'running' | 'succeeded' | 'partial_failure' | 'failed'

export interface RunSummary {
  id: string
  status: RunStatus
  fire_reason: string
  actor: string | null
  started_at: string | null
  finished_at: string | null
  duration_ms: number | null
  error_kind: string | null
  error_msg: string | null
  created_at: string
  schedule_name: string
  skill_path: string
  owner: string
  repo_name: string
}

export interface RunDetail extends RunSummary {
  tokens_in: number | null
  tokens_out: number | null
  cost_cents: number | null
  writeback_commit_sha: string | null
}

export interface RunEvent {
  id: number
  run_id: string
  ts: string
  level: string
  event_type: string
  payload_json: unknown
}

export interface SecretMeta {
  name: string
  version: number
  last_updated: string
  last_used?: string
}

export interface Me {
  login: string
  role: 'admin' | 'viewer'
}

export interface AuditEntry {
  id: number
  ts: string
  actor?: string
  action: string
  target_kind?: string
  target_id?: string
  detail: unknown
}
