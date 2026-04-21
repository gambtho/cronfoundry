// web/src/pages/Runs.tsx
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { RunStatusBadge } from '../components/RunStatusBadge'
import type { RunSummary, RunEvent } from '../lib/types'

function eventColorClass(ev: RunEvent): string {
  if (ev.event_type === 'secret.denied') return 'text-yellow-400 font-semibold'
  if (ev.event_type === 'secret.fetched') return 'text-emerald-500'
  if (ev.event_type === 'manifest.set') return 'text-sky-400'
  if (ev.level === 'error') return 'text-red-400'
  return ''
}

// eventName narrows the unknown payload_json to surface a "name" field when
// the payload is a plain object that has one. Returns null otherwise.
function eventName(payload: unknown): string | null {
  if (typeof payload !== 'object' || payload === null) return null
  if (!('name' in payload)) return null
  const v = (payload as { name: unknown }).name
  return typeof v === 'string' ? v : null
}

export default function Runs() {
  const [selected, setSelected] = useState<RunSummary | null>(null)
  const { data: runs = [], isLoading } = useQuery({
    queryKey: ['runs'],
    queryFn: () => api.runs.list({ limit: 100 }),
    refetchInterval: 10_000,
  })

  if (isLoading) return <div className="text-gray-400">Loading…</div>

  return (
    <div className="flex gap-6">
      <div className="flex-1 min-w-0">
        <h1 className="text-2xl font-bold mb-4">Run History</h1>
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-gray-500 border-b border-gray-800">
              <th className="pb-2 pr-4">Status</th>
              <th className="pb-2 pr-4">Schedule</th>
              <th className="pb-2 pr-4">Started</th>
              <th className="pb-2 pr-4">Duration</th>
              <th className="pb-2">Reason</th>
            </tr>
          </thead>
          <tbody>
            {runs.map(r => (
              <tr
                key={r.id}
                onClick={() => setSelected(r)}
                className="border-b border-gray-800 cursor-pointer hover:bg-gray-800/50"
              >
                <td className="py-2 pr-4"><RunStatusBadge status={r.status} /></td>
                <td className="py-2 pr-4 text-white">{r.schedule_name}</td>
                <td className="py-2 pr-4 text-gray-400">
                  {r.started_at ? new Date(r.started_at).toLocaleString() : '—'}
                </td>
                <td className="py-2 pr-4 text-gray-400">
                  {r.duration_ms != null ? `${(r.duration_ms / 1000).toFixed(1)}s` : '—'}
                </td>
                <td className="py-2 text-gray-500">{r.fire_reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {selected && (
        <RunDetail runId={selected.id} onClose={() => setSelected(null)} />
      )}
    </div>
  )
}

function RunDetail({ runId, onClose }: { runId: string; onClose: () => void }) {
  const { data: run } = useQuery({
    queryKey: ['run', runId],
    queryFn: () => api.runs.get(runId),
  })
  const { data: events = [] } = useQuery({
    queryKey: ['run-events', runId],
    queryFn: () => api.runs.events(runId),
    refetchInterval:
      run?.status === 'pending' || run?.status === 'running' ? 2000 : false,
  })

  return (
    <div className="w-96 shrink-0 border-l border-gray-800 pl-6">
      <div className="flex items-center justify-between mb-4">
        <h2 className="font-semibold text-white">Run detail</h2>
        <button onClick={onClose} className="text-gray-500 hover:text-white">✕</button>
      </div>
      {run && (
        <div className="space-y-2 text-sm mb-4">
          <div>
            <span className="text-gray-500">Status: </span>
            <RunStatusBadge status={run.status} />
          </div>
          {run.tokens_in != null && (
            <div className="text-gray-400">
              Tokens: {run.tokens_in} in / {run.tokens_out} out
            </div>
          )}
          {run.cost_cents != null && (
            <div className="text-gray-400">
              Cost: ${(run.cost_cents / 100).toFixed(4)}
            </div>
          )}
          {run.error_msg && (
            <div className="text-red-400 text-xs">{run.error_msg}</div>
          )}
        </div>
      )}
      <div className="space-y-1">
        {events.map(ev => (
          <div key={ev.id} className="text-xs text-gray-400 font-mono">
            <span className="text-gray-600">
              {new Date(ev.ts).toLocaleTimeString()}{' '}
            </span>
            <span className={eventColorClass(ev)}>
              {ev.event_type}
            </span>
            {ev.event_type === 'secret.denied' && eventName(ev.payload_json) && (
              <span className="text-gray-500 ml-2">
                name={eventName(ev.payload_json)}
              </span>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
