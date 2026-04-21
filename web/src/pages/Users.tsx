import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import type { UserDTO } from '../lib/types'
import { ConfirmDialog } from '../components/ConfirmDialog'

type Role = 'admin' | 'viewer'

export default function Users() {
  const qc = useQueryClient()
  const [newLogin, setNewLogin] = useState('')
  const [newRole, setNewRole] = useState<Role>('viewer')
  const [createError, setCreateError] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<UserDTO | null>(null)
  const [opError, setOpError] = useState<string | null>(null)

  const meQuery = useQuery({ queryKey: ['me'], queryFn: api.me })
  const { data: users = [], isLoading, error } = useQuery<UserDTO[]>({
    queryKey: ['users'],
    queryFn: api.users.list,
    refetchInterval: 30_000,
  })

  const createMutation = useMutation({
    mutationFn: () => api.users.create(newLogin.trim(), newRole),
    onSuccess: () => {
      setNewLogin('')
      setNewRole('viewer')
      setCreateError(null)
      qc.invalidateQueries({ queryKey: ['users'] })
    },
    onError: (err: Error) => setCreateError(err.message),
  })

  const updateMutation = useMutation({
    mutationFn: ({ login, role }: { login: string; role: Role }) =>
      api.users.updateRole(login, role),
    onSuccess: () => {
      setOpError(null)
      qc.invalidateQueries({ queryKey: ['users'] })
    },
    // Surface backend failures (409 last_admin, 404 not_found, 500 infra)
    // — without this the <select> silently reverts on refetch and the
    // operator has no signal that the change failed.
    onError: (err: Error) => {
      setOpError(err.message)
      qc.invalidateQueries({ queryKey: ['users'] })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (login: string) => api.users.delete(login),
    onSuccess: () => {
      setPendingDelete(null)
      setOpError(null)
      qc.invalidateQueries({ queryKey: ['users'] })
    },
    // Close the confirm dialog on failure so a stale 'Delete' click can't
    // re-fire the mutation; surface the reason (last_user, self_delete, etc.).
    onError: (err: Error) => {
      setPendingDelete(null)
      setOpError(err.message)
    },
  })

  if (isLoading) return <div className="text-gray-400">Loading users…</div>
  if (error) return <div className="text-red-400">Failed to load: {(error as Error).message}</div>

  const myLogin = meQuery.data?.login

  return (
    <div>
      <h1 className="text-2xl font-bold mb-4">Users</h1>

      {opError && (
        <div className="mb-4 px-3 py-2 rounded bg-red-950 border border-red-800 text-red-300 text-sm">
          {opError}{' '}
          <button
            type="button"
            onClick={() => setOpError(null)}
            className="ml-2 text-red-400 hover:text-red-200 text-xs"
          >
            dismiss
          </button>
        </div>
      )}

      <form
        onSubmit={e => {
          e.preventDefault()
          if (!newLogin.trim()) return
          createMutation.mutate()
        }}
        className="mb-6 flex items-end gap-2"
      >
        <div>
          <label className="block text-xs text-gray-500 mb-1">GitHub login</label>
          <input
            type="text"
            value={newLogin}
            onChange={e => setNewLogin(e.target.value)}
            placeholder="octocat"
            className="bg-gray-900 border border-gray-700 rounded px-2 py-1 text-sm text-white"
          />
        </div>
        <div>
          <label className="block text-xs text-gray-500 mb-1">Role</label>
          <select
            value={newRole}
            onChange={e => setNewRole(e.target.value as Role)}
            className="bg-gray-900 border border-gray-700 rounded px-2 py-1 text-sm text-white"
          >
            <option value="admin">admin</option>
            <option value="viewer">viewer</option>
          </select>
        </div>
        <button
          type="submit"
          disabled={createMutation.isPending || !newLogin.trim()}
          className="px-3 py-1.5 rounded bg-indigo-700 hover:bg-indigo-600 disabled:bg-gray-700 text-white text-sm"
        >
          Add user
        </button>
        {createError && <span className="text-red-400 text-xs">{createError}</span>}
      </form>

      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-gray-500 border-b border-gray-800">
            <th className="pb-2 pr-4">Login</th>
            <th className="pb-2 pr-4 w-32">Role</th>
            <th className="pb-2 pr-4 w-56">Created</th>
            <th className="pb-2 pr-4 w-56">Last login</th>
            <th className="pb-2 w-24"></th>
          </tr>
        </thead>
        <tbody>
          {users.map(u => {
            const isMe = u.login === myLogin
            return (
              <tr key={u.login} className="border-b border-gray-800">
                <td className="py-2 pr-4 text-white">
                  {u.login}
                  {isMe && (
                    <span className="ml-2 text-xs px-1.5 py-0.5 rounded bg-gray-800 text-gray-400">
                      you
                    </span>
                  )}
                </td>
                <td className="py-2 pr-4">
                  <select
                    value={u.role}
                    onChange={e =>
                      updateMutation.mutate({ login: u.login, role: e.target.value as Role })
                    }
                    disabled={isMe}
                    className="bg-gray-900 border border-gray-700 rounded px-2 py-1 text-xs text-white disabled:text-gray-500"
                    title={isMe ? 'Cannot change your own role' : ''}
                  >
                    <option value="admin">admin</option>
                    <option value="viewer">viewer</option>
                  </select>
                </td>
                <td className="py-2 pr-4 text-gray-400">
                  {new Date(u.created_at).toLocaleString()}
                </td>
                <td className="py-2 pr-4 text-gray-400">
                  {u.last_login_at ? new Date(u.last_login_at).toLocaleString() : 'Never'}
                </td>
                <td className="py-2">
                  <button
                    type="button"
                    onClick={() => setPendingDelete(u)}
                    disabled={isMe}
                    className="text-xs text-red-400 hover:text-red-300 disabled:text-gray-600 disabled:hover:text-gray-600"
                    title={isMe ? 'Cannot delete your own user' : ''}
                  >
                    Delete
                  </button>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>

      {pendingDelete && (
        <ConfirmDialog
          message={`Delete user ${pendingDelete.login}? Audit history is preserved.`}
          onConfirm={() => deleteMutation.mutate(pendingDelete.login)}
          onCancel={() => setPendingDelete(null)}
        />
      )}
    </div>
  )
}
