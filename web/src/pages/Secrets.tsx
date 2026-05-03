// web/src/pages/Secrets.tsx
import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { SecretModal } from '../components/SecretModal'
import { ConfirmDialog } from '../components/ConfirmDialog'
import {
  Button,
  Card,
  DataTable,
  ErrorBanner,
  IconButton,
  PageHeader,
  Topbar,
} from '../components/ui'
import { relativeTime } from '../lib/time'
import type { SecretMeta } from '../lib/types'

/**
 * Secrets — env-var-style values injected into runs. Common ops:
 * create, rotate (write a new version), delete. Values are
 * write-only: API never returns them, only metadata.
 */
export default function Secrets() {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [rotating, setRotating] = useState<SecretMeta | null>(null)
  const [deleting, setDeleting] = useState<SecretMeta | null>(null)
  // Surfaces mutation failures (delete most importantly, but also
  // create/rotate) so the dialog flow can't leave the UI silent.
  const [opError, setOpError] = useState<string | null>(null)

  const secretsQ = useQuery({
    queryKey: ['secrets'],
    queryFn: api.secrets.list,
  })

  const create = useMutation({
    mutationFn: ({ name, value }: { name: string; value: string }) =>
      api.secrets.create(name, value),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['secrets'] })
      setCreating(false)
      setOpError(null)
    },
    onError: (err: Error) => setOpError(err.message),
  })
  const rotate = useMutation({
    mutationFn: ({ name, value }: { name: string; value: string }) =>
      api.secrets.rotate(name, value),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['secrets'] })
      setRotating(null)
      setOpError(null)
    },
    // Keep the rotate modal open so the user doesn't lose the value
    // they typed — they can retry without re-entering.
    onError: (err: Error) => setOpError(err.message),
  })
  const del = useMutation({
    mutationFn: (name: string) => api.secrets.delete(name),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['secrets'] })
      setDeleting(null)
      setOpError(null)
    },
    // Close the confirmation dialog so a stale Delete click can't
    // re-fire the mutation; surface the reason so the operator knows
    // why the row is still present.
    onError: (err: Error) => {
      setDeleting(null)
      setOpError(err.message)
    },
  })

  const secrets = secretsQ.data ?? []

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Crumb href="/settings/repos">Settings</Topbar.Crumb>
          <Topbar.Sep />
          <Topbar.Here>Secrets</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[1100px] px-6 pb-16 pt-7">
        <PageHeader
          eyebrow={{
            tone: 'ok',
            text: `${secrets.length} secret${secrets.length === 1 ? '' : 's'}`,
          }}
          title="Secrets"
          subtitle="env-var-style values injected into runs · write-only"
          actions={
            <Button variant="primary" onClick={() => setCreating(true)}>
              + Add secret
            </Button>
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
              <DataTable.HeadCell>Name</DataTable.HeadCell>
              <DataTable.HeadCell>Version</DataTable.HeadCell>
              <DataTable.HeadCell>Last updated</DataTable.HeadCell>
              <DataTable.HeadCell>Last used</DataTable.HeadCell>
              <DataTable.HeadCell align="right">Actions</DataTable.HeadCell>
            </DataTable.Head>
            <tbody>
              {secretsQ.isError ? (
                <DataTable.Message colSpan={5} tone="error">
                  Could not load secrets:{' '}
                  {secretsQ.error instanceof Error
                    ? secretsQ.error.message
                    : 'request failed'}
                  .{' '}
                  <button
                    type="button"
                    onClick={() => secretsQ.refetch()}
                    className="text-ink underline-offset-2 hover:underline"
                  >
                    Retry
                  </button>
                </DataTable.Message>
              ) : secretsQ.isLoading ? (
                <DataTable.Message colSpan={5}>
                  Loading secrets…
                </DataTable.Message>
              ) : secrets.length === 0 ? (
                <DataTable.Message colSpan={5}>
                  No secrets yet. Click <b>Add secret</b> to inject one into
                  your jobs.
                </DataTable.Message>
              ) : (
                secrets.map((s) => (
                  <DataTable.Row key={s.name}>
                    <DataTable.Cell className="text-ink">
                      <span className="text-accent-amber">⚿</span>{' '}
                      <span className="font-mono">{s.name}</span>
                    </DataTable.Cell>
                    <DataTable.Cell>v{s.version}</DataTable.Cell>
                    <DataTable.Cell>
                      {relativeTime(s.last_updated)}
                    </DataTable.Cell>
                    <DataTable.Cell>
                      {s.last_used ? relativeTime(s.last_used) : '—'}
                    </DataTable.Cell>
                    <DataTable.Cell align="right">
                      <div className="inline-flex gap-1">
                        <IconButton
                          aria-label={`Rotate secret ${s.name}`}
                          onClick={() => setRotating(s)}
                          title="Rotate"
                        >
                          ↻
                        </IconButton>
                        <IconButton
                          aria-label={`Delete secret ${s.name}`}
                          onClick={() => setDeleting(s)}
                          title="Delete"
                        >
                          ✕
                        </IconButton>
                      </div>
                    </DataTable.Cell>
                  </DataTable.Row>
                ))
              )}
            </tbody>
          </DataTable>
        </Card>

        <p className="mt-8 flex justify-between border-t border-rule pt-3 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-3">
          <span>{secrets.length} secrets</span>
          <span>{secretsQ.isFetching ? 'refreshing…' : 'live'}</span>
        </p>
      </div>

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
          title="Delete secret?"
          message={`Delete secret "${deleting.name}"? This cannot be undone.`}
          onConfirm={() => del.mutate(deleting.name)}
          onCancel={() => setDeleting(null)}
        />
      )}
    </>
  )
}
