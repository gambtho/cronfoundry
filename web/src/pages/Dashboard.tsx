// web/src/pages/Dashboard.tsx
import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { RunStatusBadge } from '../components/RunStatusBadge'
import { relativeTime } from '../lib/time'
import { ScheduleOverrideForm } from '../components/ScheduleOverrideForm'

export default function Dashboard() {
  const qc = useQueryClient()
  const { data: schedules = [], isLoading } = useQuery({
    queryKey: ['schedules'],
    queryFn: api.schedules.list,
    refetchInterval: 15_000,
  })
  const { data: runs = [] } = useQuery({
    queryKey: ['runs', { limit: 5 }],
    queryFn: () => api.runs.list({ limit: 5 }),
    refetchInterval: 10_000,
  })

  const pause = useMutation({
    mutationFn: api.schedules.pause,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })
  const resume = useMutation({
    mutationFn: api.schedules.resume,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['schedules'] }),
  })
  const runNow = useMutation({
    mutationFn: api.schedules.runNow,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['runs'] }),
  })

  const [editingSchedule, setEditingSchedule] = useState<typeof schedules[0] | null>(null)

  const lastRunBySchedule = Object.fromEntries(runs.map(r => [r.schedule_name, r]))

  if (isLoading) return <div className="text-gray-400">Loading…</div>

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Dashboard</h1>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {schedules.map(s => {
          const lastRun = lastRunBySchedule[s.name]
          return (
            <div key={s.id} className="rounded-lg border border-gray-800 bg-gray-900 p-4">
              <div className="flex items-start justify-between gap-2">
                <div>
                  <div className="font-medium text-white">{s.name}</div>
                  <div className="text-xs text-gray-500 mt-0.5">
                    {s.owner}/{s.repo_name} · {s.skill_path}
                  </div>
                  <div className="text-xs text-gray-500 mt-1">
                    {s.cron} ({s.timezone})
                  </div>
                  {s.has_ui_overrides && (
                    <span className="inline-flex items-center rounded px-1.5 py-0.5 text-xs bg-yellow-900 text-yellow-200 mt-1">
                      UI overrides active
                    </span>
                  )}
                </div>
                {lastRun && <RunStatusBadge status={lastRun.status} />}
              </div>
              {s.next_fire_at && (
                <div className="mt-2 text-xs text-gray-400">
                  Next: {new Date(s.next_fire_at).toLocaleString()}
                </div>
              )}
              {s.mcp_servers?.length > 0 && (
                <div className="mt-2">
                  <span
                    className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs bg-indigo-900 text-indigo-200"
                    title={s.mcp_servers.map(m => m.name).join(', ')}
                  >
                    🔧 {s.mcp_servers.length} tool{s.mcp_servers.length !== 1 ? 's' : ''}
                  </span>
                </div>
              )}
              <div className="mt-3 flex gap-2">
                <button
                  onClick={() => runNow.mutate(s.id)}
                  disabled={runNow.isPending}
                  className="text-xs px-2 py-1 rounded bg-indigo-700 hover:bg-indigo-600 text-white disabled:opacity-50"
                >
                  Run now
                </button>
                <button
                  onClick={() => setEditingSchedule(s)}
                  className="text-xs px-2 py-1 rounded border border-gray-600 text-gray-300 hover:bg-gray-800"
                >
                  Edit
                </button>
                {s.enabled ? (
                  <button
                    onClick={() => pause.mutate(s.id)}
                    className="text-xs px-2 py-1 rounded bg-gray-700 hover:bg-gray-600 text-white"
                  >
                    Pause
                  </button>
                ) : (
                  <button
                    onClick={() => resume.mutate(s.id)}
                    className="text-xs px-2 py-1 rounded bg-green-800 hover:bg-green-700 text-white"
                  >
                    Resume
                  </button>
                )}
                {!s.enabled && s.auto_paused_at ? (
                  <span
                    title={s.auto_pause_reason ?? undefined}
                    className="text-xs px-2 py-1 rounded bg-amber-900 text-amber-200"
                  >
                    Auto-paused · {relativeTime(s.auto_paused_at)}
                  </span>
                ) : !s.enabled ? (
                  <span className="text-xs px-2 py-1 rounded bg-gray-800 text-gray-500">
                    Paused
                  </span>
                ) : null}
              </div>
            </div>
          )
        })}
      </div>
      {editingSchedule && (
        <ScheduleOverrideForm
          schedule={editingSchedule}
          onClose={() => setEditingSchedule(null)}
        />
      )}
    </div>
  )
}
