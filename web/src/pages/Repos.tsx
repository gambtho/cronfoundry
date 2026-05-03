// web/src/pages/Repos.tsx
import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { ConfirmDialog } from '../components/ConfirmDialog'
import {
  Card,
  DataTable,
  ErrorBanner,
  IconButton,
  LinkButton,
  PageHeader,
  Pill,
  Topbar,
} from '../components/ui'
import { relativeTime } from '../lib/time'
import type { RepoConnection } from '../lib/types'

const GITHUB_APP_NAME =
  (import.meta as { env?: Record<string, string> }).env?.VITE_GITHUB_APP_NAME ??
  'your-app'

/**
 * Repos — connected GitHub repos that CronFoundry watches.
 * Living under Settings; touched weekly at most. Connect goes via
 * GitHub's app installation flow (external), so the primary action
 * is a real <a> rather than a button.
 */
export default function Repos() {
  const qc = useQueryClient()
  const [disconnecting, setDisconnecting] = useState<RepoConnection | null>(
    null,
  )
  // Surfaced in a dismissible banner when disconnect fails. Same
  // pattern as Users.tsx — without it, errors leave the dialog
  // open with no explanation.
  const [opError, setOpError] = useState<string | null>(null)

  const reposQ = useQuery({
    queryKey: ['repos'],
    queryFn: api.repos.list,
    refetchInterval: 30_000,
  })
  const disconnect = useMutation({
    mutationFn: (id: string) => api.repos.disconnect(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['repos'] })
      setDisconnecting(null)
      setOpError(null)
    },
    // Close the confirm dialog on failure so a stale Disconnect click
    // can't re-fire, and surface the reason so the operator knows.
    onError: (err: Error) => {
      setDisconnecting(null)
      setOpError(err.message)
    },
  })

  const repos = reposQ.data ?? []
  const errored = repos.filter((r) => r.last_sync_error)

  const eyebrow =
    errored.length > 0
      ? ({
          tone: 'fail',
          text: `${errored.length} repo${errored.length === 1 ? '' : 's'} with sync errors`,
        } as const)
      : ({
          tone: 'ok',
          text: `${repos.length} repo${repos.length === 1 ? '' : 's'} connected`,
        } as const)

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Crumb href="/settings/repos">Settings</Topbar.Crumb>
          <Topbar.Sep />
          <Topbar.Here>Source repos</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[1100px] px-6 pb-16 pt-7">
        <PageHeader
          eyebrow={eyebrow}
          title="Source repos"
          subtitle="GitHub repos cronfoundry watches for cron schedules"
          actions={
            <LinkButton
              variant="primary"
              href={`https://github.com/apps/${GITHUB_APP_NAME}/installations/new`}
              target="_blank"
              rel="noopener noreferrer"
            >
              + Connect repo
            </LinkButton>
          }
        />

        {opError && (
          <ErrorBanner
            message={opError}
            onDismiss={() => setOpError(null)}
          />
        )}

        <Card>
          <DataTable>
            <DataTable.Head>
              <DataTable.HeadCell>Repo</DataTable.HeadCell>
              <DataTable.HeadCell>Default branch</DataTable.HeadCell>
              <DataTable.HeadCell>Last synced</DataTable.HeadCell>
              <DataTable.HeadCell>Status</DataTable.HeadCell>
              <DataTable.HeadCell align="right">Actions</DataTable.HeadCell>
            </DataTable.Head>
            <tbody>
              {reposQ.isError ? (
                <DataTable.Message colSpan={5} tone="error">
                  Could not load repos:{' '}
                  {reposQ.error instanceof Error
                    ? reposQ.error.message
                    : 'request failed'}
                  .{' '}
                  <button
                    type="button"
                    onClick={() => reposQ.refetch()}
                    className="text-ink underline-offset-2 hover:underline"
                  >
                    Retry
                  </button>
                </DataTable.Message>
              ) : reposQ.isLoading ? (
                <DataTable.Message colSpan={5}>Loading repos…</DataTable.Message>
              ) : repos.length === 0 ? (
                <DataTable.Message colSpan={5}>
                  No repos connected yet. Click <b>Connect repo</b> to install
                  the GitHub app.
                </DataTable.Message>
              ) : (
                repos.map((r) => (
                  <DataTable.Row
                    key={r.id}
                    alert={Boolean(r.last_sync_error)}
                  >
                    <DataTable.Cell>
                      <a
                        href={`https://github.com/${r.owner}/${r.name}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-[13px] font-medium text-ink hover:text-accent-green"
                      >
                        {r.owner}/{r.name}
                      </a>
                      <span className="mt-0.5 block text-[11px] text-ink-3">
                        connected {relativeTime(r.created_at)}
                      </span>
                    </DataTable.Cell>
                    <DataTable.Cell className="text-accent-cyan">
                      {r.default_branch}
                    </DataTable.Cell>
                    <DataTable.Cell>
                      {r.last_synced_at ? relativeTime(r.last_synced_at) : 'never'}
                      <span className="mt-0.5 block text-[11px] text-ink-3">
                        every {r.sync_interval_sec}s
                      </span>
                    </DataTable.Cell>
                    <DataTable.Cell>
                      {r.last_sync_error ? (
                        <>
                          <Pill variant="fail">Sync error</Pill>
                          <span
                            className="mt-1 block max-w-[260px] truncate text-[11px] text-ink-3"
                            title={r.last_sync_error}
                          >
                            {r.last_sync_error}
                          </span>
                        </>
                      ) : (
                        <Pill variant="ok">OK</Pill>
                      )}
                    </DataTable.Cell>
                    <DataTable.Cell align="right">
                      <IconButton
                        aria-label={`Disconnect ${r.owner}/${r.name}`}
                        onClick={() => setDisconnecting(r)}
                        title="Disconnect"
                      >
                        ✕
                      </IconButton>
                    </DataTable.Cell>
                  </DataTable.Row>
                ))
              )}
            </tbody>
          </DataTable>
        </Card>

        <p className="mt-8 flex justify-between border-t border-rule pt-3 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
          <span>{repos.length} repos</span>
          <span>{reposQ.isFetching ? 'refreshing…' : 'live'}</span>
        </p>
      </div>

      {disconnecting && (
        <ConfirmDialog
          title="Disconnect repo?"
          message={`Disconnect ${disconnecting.owner}/${disconnecting.name}? Its run history is preserved.`}
          confirmLabel="Disconnect"
          onConfirm={() => disconnect.mutate(disconnecting.id)}
          onCancel={() => setDisconnecting(null)}
        />
      )}
    </>
  )
}
