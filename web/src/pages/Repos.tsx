// web/src/pages/Repos.tsx
import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { ConfirmDialog } from '../components/ConfirmDialog'
import type { RepoConnection } from '../lib/types'

const GITHUB_APP_NAME = (import.meta as { env?: Record<string, string> }).env?.VITE_GITHUB_APP_NAME ?? 'your-app'

export default function Repos() {
  const qc = useQueryClient()
  const [disconnecting, setDisconnecting] = useState<RepoConnection | null>(null)

  const { data: repos = [] } = useQuery({
    queryKey: ['repos'],
    queryFn: api.repos.list,
    refetchInterval: 30_000,
  })
  const disconnect = useMutation({
    mutationFn: (id: string) => api.repos.disconnect(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['repos'] })
      setDisconnecting(null)
    },
  })

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Connected Repos</h1>
        <a
          href={`https://github.com/apps/${GITHUB_APP_NAME}/installations/new`}
          target="_blank"
          rel="noopener noreferrer"
          className="text-sm px-3 py-1.5 rounded bg-indigo-700 hover:bg-indigo-600 text-white"
        >
          Connect repo
        </a>
      </div>
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-gray-500 border-b border-gray-800">
            <th className="pb-2 pr-4">Repo</th>
            <th className="pb-2 pr-4">Last synced</th>
            <th className="pb-2 pr-4">Status</th>
            <th className="pb-2"></th>
          </tr>
        </thead>
        <tbody>
          {repos.map(r => (
            <tr key={r.id} className="border-b border-gray-800">
              <td className="py-2 pr-4 text-white">{r.owner}/{r.name}</td>
              <td className="py-2 pr-4 text-gray-400">
                {r.last_synced_at ? new Date(r.last_synced_at).toLocaleString() : 'Never'}
              </td>
              <td className="py-2 pr-4">
                {r.last_sync_error
                  ? <span className="text-red-400 text-xs">{r.last_sync_error}</span>
                  : <span className="text-green-400 text-xs">OK</span>}
              </td>
              <td className="py-2">
                <button
                  onClick={() => setDisconnecting(r)}
                  className="text-xs text-red-400 hover:text-red-300"
                >
                  Disconnect
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {disconnecting && (
        <ConfirmDialog
          message={`Disconnect ${disconnecting.owner}/${disconnecting.name}? Run history is preserved.`}
          onConfirm={() => disconnect.mutate(disconnecting.id)}
          onCancel={() => setDisconnecting(null)}
        />
      )}
    </div>
  )
}
