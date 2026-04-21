import { useEffect, useState } from 'react'
import { api } from '../lib/api'
import type { RunEvent, RunStatus } from '../lib/types'

type Props = {
  runId: string
  status: RunStatus
}

const TERMINAL: ReadonlySet<RunStatus> = new Set([
  'succeeded',
  'partial_failure',
  'failed',
])

export default function LogTail({ runId, status }: Props) {
  const [events, setEvents] = useState<RunEvent[]>([])

  useEffect(() => {
    if (TERMINAL.has(status)) {
      let cancelled = false
      api.runs.events(runId).then(rows => {
        if (!cancelled) setEvents(rows)
      })
      return () => {
        cancelled = true
      }
    }
    const es = new EventSource(api.runs.eventsStreamURL(runId))
    es.onmessage = ev => {
      try {
        const parsed = JSON.parse(ev.data) as RunEvent
        setEvents(prev => [...prev, parsed])
      } catch {
        // malformed line — ignore
      }
    }
    es.addEventListener('done', () => {
      es.close()
    })
    return () => {
      es.close()
    }
  }, [runId, status])

  return (
    <div
      role="log"
      className="mt-4 h-64 overflow-y-auto rounded bg-black/60 p-2 font-mono text-xs text-gray-300"
    >
      {events.length === 0 ? (
        <div className="text-gray-600">Waiting for events…</div>
      ) : (
        events.map(ev => <LogRow key={ev.id} ev={ev} />)
      )}
    </div>
  )
}

function LogRow({ ev }: { ev: RunEvent }) {
  const [expanded, setExpanded] = useState(false)
  return (
    <div
      onClick={() => setExpanded(v => !v)}
      className="cursor-pointer border-b border-gray-800/50 py-0.5 hover:bg-gray-800/30"
    >
      <span className="text-gray-600">{new Date(ev.ts).toLocaleTimeString()} </span>
      <span className={ev.level === 'error' ? 'text-red-400' : 'text-gray-400'}>
        {ev.level}
      </span>{' '}
      <span className="text-gray-200">{ev.event_type}</span>
      {expanded && (
        <pre className="mt-1 whitespace-pre-wrap break-all text-gray-500">
          {JSON.stringify(ev.payload_json, null, 2)}
        </pre>
      )}
    </div>
  )
}
