// web/src/pages/Users.tsx
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import type { UserDTO } from '../lib/types'
import { ConfirmDialog } from '../components/ConfirmDialog'
import {
  Button,
  Card,
  DataTable,
  ErrorBanner,
  IconButton,
  Input,
  PageHeader,
  Pill,
  Select,
  Topbar,
} from '../components/ui'
import { relativeTime } from '../lib/time'

type Role = 'admin' | 'viewer'

/**
 * Users — admin/viewer ACL. Self-protections enforced server-side
 * (can't demote/delete your last admin or yourself); we surface the
 * resulting errors via opError so the operator sees why an action
 * was rejected.
 */
export default function Users() {
  const qc = useQueryClient()
  const [newLogin, setNewLogin] = useState('')
  const [newRole, setNewRole] = useState<Role>('viewer')
  const [createError, setCreateError] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<UserDTO | null>(null)
  const [opError, setOpError] = useState<string | null>(null)

  const meQuery = useQuery({ queryKey: ['me'], queryFn: api.me })
  const usersQ = useQuery<UserDTO[]>({
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
    // Surface backend failures (409 last_admin, 404 not_found, 500 infra).
    // Without this the <select> silently reverts on refetch and the
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
    onError: (err: Error) => {
      setPendingDelete(null)
      setOpError(err.message)
    },
  })

  const users = usersQ.data ?? []
  const myLogin = meQuery.data?.login

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Crumb href="/settings/repos">Settings</Topbar.Crumb>
          <Topbar.Sep />
          <Topbar.Here>Users</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[1100px] px-6 pb-16 pt-7">
        <PageHeader
          title="Users"
          subtitle={`${users.length} member${users.length === 1 ? '' : 's'}`}
        />

        {opError && (
          <ErrorBanner
            message={opError}
            onDismiss={() => setOpError(null)}
          />
        )}

        {/* add user form */}
        <Card>
          <Card.Header>Invite user</Card.Header>
          <Card.Body>
            <form
              onSubmit={(e) => {
                e.preventDefault()
                if (!newLogin.trim()) return
                createMutation.mutate()
              }}
              className="flex flex-wrap items-end gap-3"
            >
              <Input
                label="GitHub login"
                placeholder="octocat"
                value={newLogin}
                onChange={(e) => setNewLogin(e.target.value)}
                variant="mono"
                className="w-[200px]"
              />
              <Select
                label="Role"
                value={newRole}
                onChange={(e) => setNewRole(e.target.value as Role)}
                className="w-[140px]"
              >
                <option value="admin">admin</option>
                <option value="viewer">viewer</option>
              </Select>
              <Button
                type="submit"
                variant="primary"
                disabled={createMutation.isPending || !newLogin.trim()}
              >
                Add user
              </Button>
              {createError && (
                <span className="font-mono text-[11px] text-accent-red">
                  {createError}
                </span>
              )}
            </form>
          </Card.Body>
        </Card>

        <Card>
          <DataTable>
            <DataTable.Head>
              <DataTable.HeadCell>Login</DataTable.HeadCell>
              <DataTable.HeadCell>Role</DataTable.HeadCell>
              <DataTable.HeadCell>Created</DataTable.HeadCell>
              <DataTable.HeadCell>Last login</DataTable.HeadCell>
              <DataTable.HeadCell align="right">Actions</DataTable.HeadCell>
            </DataTable.Head>
            <tbody>
              {usersQ.isError ? (
                <DataTable.Message colSpan={5} tone="error">
                  Could not load users:{' '}
                  {usersQ.error instanceof Error
                    ? usersQ.error.message
                    : 'request failed'}
                  .{' '}
                  <button
                    type="button"
                    onClick={() => usersQ.refetch()}
                    className="text-ink underline-offset-2 hover:underline"
                  >
                    Retry
                  </button>
                </DataTable.Message>
              ) : usersQ.isLoading ? (
                <DataTable.Message colSpan={5}>
                  Loading users…
                </DataTable.Message>
              ) : users.length === 0 ? (
                <DataTable.Message colSpan={5}>No users yet.</DataTable.Message>
              ) : (
                users.map((u) => {
                  const isMe = u.login === myLogin
                  return (
                    <DataTable.Row key={u.login}>
                      <DataTable.Cell className="text-ink">
                        <span className="font-mono">{u.login}</span>
                        {isMe && (
                          <Pill variant="skip" className="ml-2">
                            you
                          </Pill>
                        )}
                      </DataTable.Cell>
                      <DataTable.Cell>
                        <select
                          value={u.role}
                          onChange={(e) =>
                            updateMutation.mutate({
                              login: u.login,
                              role: e.target.value as Role,
                            })
                          }
                          disabled={isMe}
                          aria-label={`Role for ${u.login}`}
                          title={
                            isMe ? 'Cannot change your own role' : undefined
                          }
                          className="rounded border border-rule bg-bg px-2 py-1 text-[12px] text-ink focus:border-ink-3 focus:outline-none disabled:cursor-not-allowed disabled:opacity-50"
                        >
                          <option value="admin">admin</option>
                          <option value="viewer">viewer</option>
                        </select>
                      </DataTable.Cell>
                      <DataTable.Cell>
                        {relativeTime(u.created_at)}
                      </DataTable.Cell>
                      <DataTable.Cell>
                        {u.last_login_at
                          ? relativeTime(u.last_login_at)
                          : 'never'}
                      </DataTable.Cell>
                      <DataTable.Cell align="right">
                        <IconButton
                          aria-label={`Delete user ${u.login}`}
                          disabled={isMe}
                          onClick={() => setPendingDelete(u)}
                          title={
                            isMe ? 'Cannot delete your own user' : 'Delete'
                          }
                        >
                          ✕
                        </IconButton>
                      </DataTable.Cell>
                    </DataTable.Row>
                  )
                })
              )}
            </tbody>
          </DataTable>
        </Card>

        <p className="mt-8 flex justify-between border-t border-rule pt-3 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
          <span>{users.length} users</span>
          <span>{usersQ.isFetching ? 'refreshing…' : 'live'}</span>
        </p>
      </div>

      {pendingDelete && (
        <ConfirmDialog
          title="Delete user?"
          message={`Delete user ${pendingDelete.login}? Audit history is preserved.`}
          onConfirm={() => deleteMutation.mutate(pendingDelete.login)}
          onCancel={() => setPendingDelete(null)}
        />
      )}
    </>
  )
}
