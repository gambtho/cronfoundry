// web/src/pages/Secrets.tsx
import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { SecretModal } from '../components/SecretModal'
import { ConfirmDialog } from '../components/ConfirmDialog'
import type { SecretMeta } from '../lib/types'

export default function Secrets() {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [rotating, setRotating] = useState<SecretMeta | null>(null)
  const [deleting, setDeleting] = useState<SecretMeta | null>(null)

  const { data: secrets = [] } = useQuery({
    queryKey: ['secrets'],
    queryFn: api.secrets.list,
  })
  const create = useMutation({
    mutationFn: ({ name, value }: { name: string; value: string }) =>
      api.secrets.create(name, value),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['secrets'] })
      setCreating(false)
    },
  })
  const rotate = useMutation({
    mutationFn: ({ name, value }: { name: string; value: string }) =>
      api.secrets.rotate(name, value),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['secrets'] })
      setRotating(null)
    },
  })
  const del = useMutation({
    mutationFn: (name: string) => api.secrets.delete(name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['secrets'] })
      setDeleting(null)
    },
  })

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Secrets</h1>
        <button
          onClick={() => setCreating(true)}
          className="text-sm px-3 py-1.5 rounded bg-indigo-700 hover:bg-indigo-600 text-white"
        >
          Add secret
        </button>
      </div>
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-gray-500 border-b border-gray-800">
            <th className="pb-2 pr-4">Name</th>
            <th className="pb-2 pr-4">Version</th>
            <th className="pb-2 pr-4">Last updated</th>
            <th className="pb-2 pr-4">Last used</th>
            <th className="pb-2"></th>
          </tr>
        </thead>
        <tbody>
          {secrets.map(s => (
            <tr key={s.name} className="border-b border-gray-800">
              <td className="py-2 pr-4 text-white font-mono">{s.name}</td>
              <td className="py-2 pr-4 text-gray-400">v{s.version}</td>
              <td className="py-2 pr-4 text-gray-400">
                {new Date(s.last_updated).toLocaleString()}
              </td>
              <td className="py-2 pr-4 text-gray-400">
                {s.last_used ? new Date(s.last_used).toLocaleString() : '—'}
              </td>
              <td className="py-2">
                <div className="flex gap-2">
                  <button
                    onClick={() => setRotating(s)}
                    className="text-xs text-indigo-400 hover:text-indigo-300"
                  >
                    Rotate
                  </button>
                  <button
                    onClick={() => setDeleting(s)}
                    className="text-xs text-red-400 hover:text-red-300"
                  >
                    Delete
                  </button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {creating && (
        <SecretModal
          mode="create"
          onSubmit={(name, value) => create.mutate({ name, value })}
          onCancel={() => setCreating(false)}
        />
      )}
      {rotating && (
        <SecretModal
          mode="rotate"
          name={rotating.name}
          onSubmit={(name, value) => rotate.mutate({ name, value })}
          onCancel={() => setRotating(null)}
        />
      )}
      {deleting && (
        <ConfirmDialog
          message={`Delete secret "${deleting.name}"? This cannot be undone.`}
          onConfirm={() => del.mutate(deleting.name)}
          onCancel={() => setDeleting(null)}
        />
      )}
    </div>
  )
}
