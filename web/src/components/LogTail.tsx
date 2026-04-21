import { useEffect, useRef, useState } from 'react'
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
const STICKY_THRESHOLD_PX = 50
const RETRY_CAP = 5

export default function LogTail({ runId, status }: Props) {
  const [events, setEvents] = useState<RunEvent[]>([])
  const [sticky, setSticky] = useState(true)
  const scrollRef = useRef<HTMLDivElement>(null)
  const retryRef = useRef(0)
  const [lost, setLost] = useState(false)

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
    retryRef.current = 0
    const es = new EventSource(api.runs.eventsStreamURL(runId))
    es.onmessage = ev => {
      try {
        const parsed = JSON.parse(ev.data) as RunEvent
        setEvents(prev => [...prev, parsed])
      } catch {
        // malformed line — ignore
      }
    }
    es.onerror = () => {
      retryRef.current++
      if (retryRef.current >= RETRY_CAP) {
        es.close()
        setLost(true)
      }
    }
    es.addEventListener('done', () => {
      es.close()
    })
    return () => {
      es.close()
    }
  }, [runId, status])

  useEffect(() => {
    if (!sticky) return
    const el = scrollRef.current
    if (!el) return
    el.scrollTo?.({ top: el.scrollHeight })
  }, [events, sticky])

  function handleScroll() {
    const el = scrollRef.current
    if (!el) return
    const atBottom =
      el.scrollHeight - el.scrollTop - el.clientHeight < STICKY_THRESHOLD_PX
    setSticky(atBottom)
  }

  return (
    <div
      ref={scrollRef}
      onScroll={handleScroll}
      role="log"
      className="mt-4 h-64 overflow-y-auto rounded bg-black/60 p-2 font-mono text-xs text-gray-300"
    >
      {lost && (
        <div className="mb-1 text-red-400">
          connection lost — reload to retry
        </div>
      )}
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
