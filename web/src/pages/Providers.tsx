// web/src/pages/Providers.tsx
import { useEffect, useRef, useState } from 'react'
import { Button, Card, Input, PageHeader, Pill, Topbar } from '../components/ui'

/**
 * Providers — connect external LLM providers. Today: GitHub Copilot
 * Enterprise via OAuth device flow. The flow is multi-stage so the
 * card content swaps depending on `flow.phase`.
 *
 * Concurrency notes:
 *   - A monotonic run-token guards against overlapping connect
 *     attempts. If the user clicks Connect twice (or remounts the
 *     page mid-flow), the older loop's writes are dropped.
 *   - On unmount, the active token is invalidated so a still-running
 *     poll loop can't call setState after the component is gone.
 */

type FlowPhase =
  | { phase: 'idle' }
  | {
      phase: 'authorizing'
      userCode: string
      verificationUri: string
      deviceCode: string
    }
  | {
      phase: 'polling'
      userCode: string
      verificationUri: string
      deviceCode: string
    }
  | { phase: 'success'; prefix: string }
  | { phase: 'error'; message: string }

export default function Providers() {
  const [prefix, setPrefix] = useState('copilot')
  const [flow, setFlow] = useState<FlowPhase>({ phase: 'idle' })

  // Token of the most recent run. Each startConnect bumps it; any
  // outstanding poll loop checks the token at every await boundary
  // and bails on mismatch. Set to 0 on unmount to invalidate all
  // in-flight loops at once.
  const runIdRef = useRef(0)

  useEffect(() => {
    return () => {
      runIdRef.current = 0
    }
  }, [])

  async function startConnect() {
    // Prevent overlap: ignore the click if a connect attempt is
    // currently in-flight. Operators can hit Cancel — well, there's
    // no Cancel today, but they can refresh — and try again.
    if (flow.phase === 'authorizing' || flow.phase === 'polling') return

    const myRunId = runIdRef.current + 1
    runIdRef.current = myRunId
    setFlow({ phase: 'idle' })

    try {
      const res = await fetch('/api/copilot/connect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ prefix }),
      })
      if (runIdRef.current !== myRunId) return
      if (!res.ok) {
        setFlow({ phase: 'error', message: 'Failed to start authorization.' })
        return
      }
      const data = await res.json()
      if (runIdRef.current !== myRunId) return
      setFlow({
        phase: 'authorizing',
        userCode: data.user_code,
        verificationUri: data.verification_uri,
        deviceCode: data.device_code,
      })
      poll(myRunId, data.device_code, data.user_code, data.verification_uri)
    } catch {
      if (runIdRef.current !== myRunId) return
      setFlow({ phase: 'error', message: 'Network error. Try again.' })
    }
  }

  async function poll(
    runId: number,
    deviceCode: string,
    userCode: string,
    verificationUri: string,
  ) {
    if (runIdRef.current !== runId) return
    setFlow({ phase: 'polling', deviceCode, userCode, verificationUri })
    const deadline = Date.now() + 5 * 60 * 1000
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 5000))
      if (runIdRef.current !== runId) return
      try {
        const res = await fetch(`/api/copilot/connect/${deviceCode}/poll`)
        if (runIdRef.current !== runId) return
        if (!res.ok) {
          setFlow({
            phase: 'error',
            message: 'Network error during authorization.',
          })
          return
        }
        const data = await res.json()
        if (runIdRef.current !== runId) return
        if (data.status === 'success') {
          setFlow({ phase: 'success', prefix })
          return
        }
        if (data.status === 'error') {
          const messages: Record<string, string> = {
            access_denied: 'Authorization was declined. Try again.',
            expired: 'Code expired. Try again.',
          }
          setFlow({
            phase: 'error',
            message: messages[data.error] ?? `Error: ${data.error}`,
          })
          return
        }
        // pending — keep polling
      } catch {
        if (runIdRef.current !== runId) return
        setFlow({
          phase: 'error',
          message: 'Network error during authorization.',
        })
        return
      }
    }
    if (runIdRef.current !== runId) return
    setFlow({ phase: 'error', message: 'Timed out waiting for authorization.' })
  }

  return (
    <>
      <Topbar>
        <Topbar.Crumbs>
          <Topbar.Crumb href="/settings/repos">Settings</Topbar.Crumb>
          <Topbar.Sep />
          <Topbar.Here>Providers</Topbar.Here>
        </Topbar.Crumbs>
        <Topbar.Spacer />
        <Topbar.Search />
      </Topbar>

      <div className="w-full max-w-[720px] px-6 pb-16 pt-7">
        <PageHeader
          title="Providers"
          subtitle="connect the LLM accounts your jobs invoke"
        />

        <Card>
          <Card.Header>
            <span>GitHub Copilot Enterprise</span>
            {flow.phase === 'success' && (
              <Pill variant="ok" className="ml-auto normal-case tracking-normal">
                Connected
              </Pill>
            )}
          </Card.Header>
          <Card.Body>
            <p className="m-0 mb-4 text-[13px] text-ink-2">
              Connect a GitHub Copilot Enterprise account so your jobs can use
              it as an LLM provider. You'll authorize cronfoundry in your
              browser on github.com.
            </p>

            {(flow.phase === 'idle' || flow.phase === 'error') && (
              <div className="flex flex-col gap-4">
                <Input
                  label="Secret prefix"
                  value={prefix}
                  onChange={(e) => setPrefix(e.target.value)}
                  variant="mono"
                  placeholder="copilot"
                  hint="Tokens are stored as ${prefix}_access and ${prefix}_refresh."
                  className="max-w-[240px]"
                />
                {flow.phase === 'error' && (
                  <p className="m-0 font-mono text-[12px] text-accent-red">
                    {flow.message}
                  </p>
                )}
                <Button
                  variant="primary"
                  disabled={!prefix.trim()}
                  onClick={startConnect}
                  className="self-start"
                >
                  Connect
                </Button>
              </div>
            )}

            {(flow.phase === 'authorizing' || flow.phase === 'polling') && (
              <div className="flex flex-col gap-3">
                <p className="m-0 text-[13px] text-ink-2">
                  Enter this code at{' '}
                  <a
                    href={flow.verificationUri}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="border-b border-dotted border-ink-3 text-ink hover:text-accent-green hover:border-accent-green"
                  >
                    {flow.verificationUri}
                  </a>
                  :
                </p>
                <p className="m-0 rounded border border-rule bg-bg-3 py-3 text-center font-mono text-[24px] tracking-[0.4em] text-accent-cyan">
                  {flow.userCode}
                </p>
                <p className="m-0 flex items-center gap-2 font-mono text-[11px] text-ink-3">
                  <Pill variant="run">Waiting</Pill>
                  Polling for authorization…
                </p>
              </div>
            )}

            {flow.phase === 'success' && (
              <div className="flex flex-col gap-3">
                <p className="m-0 font-mono text-[12px] text-accent-green">
                  ✓ Copilot Enterprise connected.
                </p>
                <pre className="m-0 overflow-x-auto rounded bg-[#06080a] p-3 font-mono text-[11.5px] text-ink">
                  <span className="text-ink-3">provider: </span>
                  <span className="text-accent-cyan">copilot-enterprise</span>
                  {'\n'}
                  <span className="text-ink-3">copilot_prefix: </span>
                  <span className="text-accent-cyan">{flow.prefix}</span>
                </pre>
                <p className="m-0 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
                  add the above to your cronfoundry.yaml
                </p>
              </div>
            )}
          </Card.Body>
        </Card>
      </div>
    </>
  )
}
