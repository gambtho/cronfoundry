// web/src/pages/Audit.tsx
import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import {
  Card,
  DataTable,
  Input,
  PageHeader,
  Select,
  Topbar,
} from '../components/ui'
import type { AuditEntry } from '../lib/types'

/**
 * Audit — chronological log of write actions. Group by action prefix
 * so the operator can scan colours quickly: secrets / repos /
 * schedules / users.
 */

function actionTone(action: string): string {
  if (action.startsWith('secret.')) return 'text-accent-amber'
  if (action.startsWith('repo.')) return 'text-accent-cyan'
  if (action.startsWith('schedule.')) return 'text-accent-green'
  if (action.startsWith('user.')) return 'text-[#e8a3c8]' // not a token; rare
  return 'text-ink-2'
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

type Group = 'all' | 'secret' | 'repo' | 'schedule' | 'user'

export default function Audit() {
  const [group, setGroup] = useState<Group>('all')
  const [query, setQuery] = useState('')

  const auditQ = useQuery<AuditEntry[]>({
    queryKey: ['audit'],
    queryFn: () => api.audit.list({ limit: 200 }),
    refetchInterval: 30_000,
  })

  const rows = auditQ.data ?? []

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    return rows.filter((r) => {
      if (group !== 'all' && !r.action.startsWith(`${group}.`)) return false
      if (q === '') return true
      const detail = formatDetail(r.detail).toLowerCase()
      return (
        r.action.toLowerCase().includes(q) ||
        (r.actor?.toLowerCase().includes(q) ?? false) ||
        (r.target_kind?.toLowerCase().includes(q) ?? false) ||
        (r.target_id?.toLowerCase().includes(q) ?? false) ||
        detail.includes(q)
      )
    })
  }, [rows, group, query])

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Crumb href="/settings/repos">Settings</Topbar.Crumb>
          <Topbar.Sep />
          <Topbar.Here>Audit log</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[1320px] px-6 pb-16 pt-7">
        <PageHeader
          title="Audit log"
          subtitle="every write action on cronfoundry, newest first"
        />

        <div className="mb-3.5 flex flex-wrap items-end gap-3 py-2.5">
          <Select
            label="Group"
            value={group}
            onChange={(e) => setGroup(e.target.value as Group)}
            className="w-[160px]"
          >
            <option value="all">all</option>
            <option value="secret">secret</option>
            <option value="repo">repo</option>
            <option value="schedule">schedule</option>
            <option value="user">user</option>
          </Select>
          <Input
            label="Filter"
            placeholder="action, actor, target, detail…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="w-[280px]"
          />
        </div>

        <Card>
          <DataTable>
            <DataTable.Head>
              <DataTable.HeadCell className="w-48">Time</DataTable.HeadCell>
              <DataTable.HeadCell className="w-32">Actor</DataTable.HeadCell>
              <DataTable.HeadCell>Action</DataTable.HeadCell>
              <DataTable.HeadCell className="w-40">Target</DataTable.HeadCell>
              <DataTable.HeadCell>Detail</DataTable.HeadCell>
            </DataTable.Head>
            <tbody>
              {auditQ.isError ? (
                <DataTable.Message colSpan={5} tone="error">
                  Could not load audit:{' '}
                  {auditQ.error instanceof Error
                    ? auditQ.error.message
                    : 'request failed'}
                  .{' '}
                  <button
                    type="button"
                    onClick={() => auditQ.refetch()}
                    className="text-ink underline-offset-2 hover:underline"
                  >
                    Retry
                  </button>
                </DataTable.Message>
              ) : auditQ.isLoading ? (
                <DataTable.Message colSpan={5}>
                  Loading audit log…
                </DataTable.Message>
              ) : visible.length === 0 ? (
                <DataTable.Message colSpan={5}>
                  {rows.length === 0
                    ? 'No audit events yet.'
                    : 'No events match this filter.'}
                </DataTable.Message>
              ) : (
                visible.map((row) => {
                  const detail = formatDetail(row.detail)
                  return (
                    <DataTable.Row key={row.id}>
                      <DataTable.Cell className="text-ink-3">
                        {new Date(row.ts).toLocaleString(undefined, {
                          dateStyle: 'medium',
                          timeStyle: 'medium',
                        })}
                      </DataTable.Cell>
                      <DataTable.Cell>
                        {row.actor || (
                          <span className="italic text-ink-3">system</span>
                        )}
                      </DataTable.Cell>
                      <DataTable.Cell>
                        <span className={`font-mono ${actionTone(row.action)}`}>
                          {row.action}
                        </span>
                      </DataTable.Cell>
                      <DataTable.Cell className="text-ink-3">
                        {row.target_kind ? (
                          <>
                            {row.target_kind}
                            {row.target_id && (
                              <span className="ml-1 text-ink-4">
                                {row.target_id.slice(0, 8)}
                              </span>
                            )}
                          </>
                        ) : (
                          ''
                        )}
                      </DataTable.Cell>
                      <DataTable.Cell
                        className="text-ink-3 align-top"
                        title={detail || undefined}
                      >
                        {truncate(detail)}
                      </DataTable.Cell>
                    </DataTable.Row>
                  )
                })
              )}
            </tbody>
          </DataTable>
        </Card>

        <p className="mt-8 flex justify-between border-t border-rule pt-3 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
          <span>
            showing {visible.length} of {rows.length} events
          </span>
          <span>{auditQ.isFetching ? 'refreshing…' : 'live'}</span>
        </p>
      </div>
    </>
  )
}
