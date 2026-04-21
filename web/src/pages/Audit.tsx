import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import type { AuditEntry } from '../lib/types'

function actionColor(action: string): string {
  if (action.startsWith('secret.')) return 'text-yellow-300'
  if (action.startsWith('repo.')) return 'text-sky-300'
  if (action.startsWith('schedule.')) return 'text-emerald-300'
  if (action.startsWith('user.')) return 'text-pink-300'
  return 'text-gray-300'
}

function formatDetail(detail: unknown): string {
  if (detail == null) return ''
  if (typeof detail !== 'object') return String(detail)
  const s = JSON.stringify(detail)
  return s === '{}' ? '' : s
}

function truncate(s: string, max = 80): string {
  return s.length > max ? s.slice(0, max - 1) + '…' : s
}

export default function Audit() {
  const { data: rows = [], isLoading, error } = useQuery<AuditEntry[]>({
    queryKey: ['audit'],
    queryFn: () => api.audit.list({ limit: 200 }),
    refetchInterval: 30_000,
  })

  if (isLoading) return <div className="text-gray-400">Loading audit log…</div>
  if (error) return <div className="text-red-400">Failed to load: {(error as Error).message}</div>

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">Audit Log</h1>
      {rows.length === 0 ? (
        <div className="text-gray-400 text-sm">No audit events yet.</div>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-gray-500 border-b border-gray-800">
              <th className="pb-2 pr-4 w-48">Time</th>
              <th className="pb-2 pr-4 w-32">Actor</th>
              <th className="pb-2 pr-4">Action</th>
              <th className="pb-2 pr-4 w-40">Target</th>
              <th className="pb-2">Detail</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(row => {
              const detail = formatDetail(row.detail)
              return (
                <tr key={row.id} className="border-b border-gray-800 align-top">
                  <td className="py-2 pr-4 text-gray-400 font-mono text-xs">
                    {new Date(row.ts).toLocaleString()}
                  </td>
                  <td className="py-2 pr-4 text-gray-300">
                    {row.actor || <span className="text-gray-600 italic">system</span>}
                  </td>
                  <td className={`py-2 pr-4 font-mono text-xs ${actionColor(row.action)}`}>
                    {row.action}
                  </td>
                  <td className="py-2 pr-4 text-gray-400 text-xs">
                    {row.target_kind
                      ? <>
                          {row.target_kind}
                          {row.target_id && (
                            <span className="text-gray-600 ml-1">{row.target_id.slice(0, 8)}</span>
                          )}
                        </>
                      : ''}
                  </td>
                  <td className="py-2 text-gray-500 font-mono text-xs" title={detail}>
                    {truncate(detail)}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </div>
  )
}
