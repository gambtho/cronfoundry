// web/src/pages/Providers.tsx
import { useState } from 'react'

type FlowPhase =
  | { phase: 'idle' }
  | { phase: 'authorizing'; userCode: string; verificationUri: string; deviceCode: string }
  | { phase: 'polling'; userCode: string; verificationUri: string; deviceCode: string }
  | { phase: 'success'; prefix: string }
  | { phase: 'error'; message: string }

export default function Providers() {
  const [prefix, setPrefix] = useState('copilot')
  const [flow, setFlow] = useState<FlowPhase>({ phase: 'idle' })

  async function startConnect() {
    setFlow({ phase: 'idle' })
    try {
      const res = await fetch('/api/copilot/connect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ prefix }),
      })
      if (!res.ok) {
        setFlow({ phase: 'error', message: 'Failed to start authorization.' })
        return
      }
      const data = await res.json()
      const state: FlowPhase = {
        phase: 'authorizing',
        userCode: data.user_code,
        verificationUri: data.verification_uri,
        deviceCode: data.device_code,
      }
      setFlow(state)
      poll(data.device_code, data.user_code, data.verification_uri)
    } catch {
      setFlow({ phase: 'error', message: 'Network error. Try again.' })
    }
  }

  async function poll(deviceCode: string, userCode: string, verificationUri: string) {
    setFlow({ phase: 'polling', deviceCode, userCode, verificationUri })
    const deadline = Date.now() + 5 * 60 * 1000
    while (Date.now() < deadline) {
      await new Promise(r => setTimeout(r, 5000))
      try {
        const res = await fetch(`/api/copilot/connect/${deviceCode}/poll`)
        if (!res.ok) {
          setFlow({ phase: 'error', message: 'Network error during authorization.' })
          return
        }
        const data = await res.json()
        if (data.status === 'success') {
          setFlow({ phase: 'success', prefix })
          return
        }
        if (data.status === 'error') {
          const messages: Record<string, string> = {
            access_denied: 'Authorization was declined. Try again.',
            expired: 'Code expired. Try again.',
          }
          setFlow({ phase: 'error', message: messages[data.error] ?? `Error: ${data.error}` })
          return
        }
        // pending — keep polling
      } catch {
        setFlow({ phase: 'error', message: 'Network error during authorization.' })
        return
      }
    }
    setFlow({ phase: 'error', message: 'Timed out waiting for authorization.' })
  }

  return (
    <div>
      <h1 className="text-xl font-semibold mb-6">Providers</h1>

      <div className="border border-gray-800 rounded-lg p-6 max-w-lg">
        <h2 className="font-medium mb-1">GitHub Copilot Enterprise</h2>
        <p className="text-sm text-gray-400 mb-4">
          Connect your GitHub Copilot Enterprise account. You will be asked to authorize
          CronFoundry in your browser.
        </p>

        {(flow.phase === 'idle' || flow.phase === 'error') && (
          <div className="space-y-4">
            <div className="flex items-center gap-3">
              <label className="text-sm text-gray-300 w-28">Secret prefix</label>
              <input
                type="text"
                value={prefix}
                onChange={e => setPrefix(e.target.value)}
                className="bg-gray-800 border border-gray-700 rounded px-3 py-1.5 text-sm text-white w-40 focus:outline-none focus:border-gray-500"
                placeholder="copilot"
              />
            </div>
            {flow.phase === 'error' && (
              <p className="text-sm text-red-400">{flow.message}</p>
            )}
            <button
              onClick={startConnect}
              disabled={!prefix}
              className="bg-gray-700 hover:bg-gray-600 disabled:opacity-40 text-white text-sm px-4 py-2 rounded"
            >
              Connect
            </button>
          </div>
        )}

        {(flow.phase === 'authorizing' || flow.phase === 'polling') && (
          <div className="space-y-3">
            <p className="text-sm text-gray-300">
              Enter this code at{' '}
              <a
                href={flow.verificationUri}
                target="_blank"
                rel="noopener noreferrer"
                className="text-blue-400 underline"
              >
                {flow.verificationUri}
              </a>
              :
            </p>
            <p className="font-mono text-2xl tracking-widest text-center py-3 bg-gray-800 rounded">
              {flow.userCode}
            </p>
            <p className="text-sm text-gray-400">Waiting for authorization…</p>
          </div>
        )}

        {flow.phase === 'success' && (
          <div className="space-y-2">
            <p className="text-sm text-green-400">✓ Copilot Enterprise connected.</p>
            <p className="text-sm text-gray-400">
              Use <code className="text-gray-200">provider: copilot-enterprise</code> and{' '}
              <code className="text-gray-200">copilot_prefix: {flow.prefix}</code> in your{' '}
              <code className="text-gray-200">cronfoundry.yaml</code>.
            </p>
          </div>
        )}
      </div>
    </div>
  )
}
