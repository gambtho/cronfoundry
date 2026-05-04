// web/src/components/ChatDock.tsx
import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { cn } from '../lib/cn'

/**
 * ChatDock — floating in-app assistant.
 *
 * The dock is a fixed-position panel anchored to the bottom-right of the
 * viewport. It collapses to a single round button when the user isn't
 * actively chatting, and expands into a vertical conversation panel.
 *
 * Wire protocol:
 *   - GET  /api/chat/info     → { enabled, model }
 *     If enabled=false the dock renders nothing — feature is opt-in via
 *     server-side env vars.
 *   - POST /api/chat/stream   → SSE stream of named events:
 *       event: token       data: { delta }      → append to the in-flight assistant turn
 *       event: tool_start  data: { name, input } → render a "calling X" pill
 *       event: tool_end    data: { name, error? } → mark the pill resolved
 *       event: error       data: { message }    → surface as an inline error
 *       event: done        data: { final, ... } → finalize the assistant turn
 *
 * The page-context string (e.g. "/jobs/abc-123") is sent on every turn so
 * questions like "why did this run fail?" work without copy-paste.
 */

type Role = 'user' | 'assistant'
type ToolPill = { name: string; status: 'running' | 'ok' | 'error'; errorMsg?: string }

interface ChatTurn {
  role: Role
  content: string
  // Tool calls observed during this assistant turn. Only populated for
  // role=assistant turns; user turns leave it empty.
  tools?: ToolPill[]
}

export default function ChatDock() {
  const [open, setOpen] = useState(false)
  const [input, setInput] = useState('')
  const [turns, setTurns] = useState<ChatTurn[]>([])
  const [streaming, setStreaming] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [needsReconnect, setNeedsReconnect] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const location = useLocation()

  // Server-side feature flag. If chat is disabled, render nothing.
  const { data: info } = useQuery({
    queryKey: ['chat-info'],
    queryFn: api.chat.info,
    // Cache for the lifetime of the tab — chat config is set at startup.
    staleTime: Infinity,
    retry: 0,
  })

  // Auto-scroll the conversation when new content arrives.
  useEffect(() => {
    if (!scrollRef.current) return
    scrollRef.current.scrollTop = scrollRef.current.scrollHeight
  }, [turns, streaming])

  // Cancel any in-flight stream when the component unmounts. Without this
  // a slow turn keeps running on the server even after the user navigates
  // away from the SPA, eating tokens.
  useEffect(() => {
    return () => abortRef.current?.abort()
  }, [])

  if (!info?.enabled) return null

  function clearChat() {
    abortRef.current?.abort()
    setTurns([])
    setError(null)
    setNeedsReconnect(false)
    setStreaming(false)
  }

  async function send() {
    const text = input.trim()
    if (!text || streaming) return
    setInput('')
    setError(null)
    setNeedsReconnect(false)

    const nextHistory: ChatTurn[] = [
      ...turns,
      { role: 'user', content: text },
      { role: 'assistant', content: '', tools: [] },
    ]
    setTurns(nextHistory)
    setStreaming(true)

    const ac = new AbortController()
    abortRef.current = ac

    try {
      // CSRF: read the cf_csrf cookie and echo it as a header. Same
      // pattern as apiFetch in lib/api.ts; we can't reuse apiFetch
      // because it consumes res.json() and we need the raw stream.
      const csrf = document.cookie
        .split('; ')
        .find((c) => c.startsWith('cf_csrf='))
        ?.slice('cf_csrf='.length)

      // Send the prior turns (without the empty placeholder we just
      // pushed) so the server has the conversation context.
      const messages = nextHistory.slice(0, -1).map((t) => ({
        role: t.role,
        content: t.content,
      }))

      const res = await fetch(api.chat.streamURL(), {
        method: 'POST',
        credentials: 'include',
        signal: ac.signal,
        headers: {
          'Content-Type': 'application/json',
          ...(csrf ? { 'X-CSRF-Token': csrf } : {}),
        },
        body: JSON.stringify({
          messages,
          page_context: location.pathname,
        }),
      })

      if (!res.ok || !res.body) {
        const body = (await res
          .json()
          .catch(() => ({ error: res.statusText }))) as {
          error?: string
          code?: string
        }
        // Provider auth expired (401 with a structured code, e.g.
        // "provider_auth_expired") — surface a calmer reconnect hint
        // instead of the red error chip so the operator knows where to go.
        if (res.status === 401 && body.code) {
          setNeedsReconnect(true)
        }
        throw new Error(body.error ?? `HTTP ${res.status}`)
      }

      // Parse the SSE stream incrementally. We split on the "\n\n"
      // event boundary and process each event line-by-line. Anything
      // trailing without a terminator stays in the buffer for the
      // next chunk.
      const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
      let buf = ''
      while (true) {
        const { value, done } = await reader.read()
        if (done) break
        buf += value
        let sepIdx: number
        while ((sepIdx = buf.indexOf('\n\n')) !== -1) {
          const block = buf.slice(0, sepIdx)
          buf = buf.slice(sepIdx + 2)
          handleEvent(block)
        }
      }
    } catch (e) {
      if ((e as Error).name === 'AbortError') return
      setError((e as Error).message)
    } finally {
      setStreaming(false)
      abortRef.current = null
    }
  }

  function handleEvent(block: string) {
    let eventName = 'message'
    let dataRaw = ''
    for (const line of block.split('\n')) {
      if (line.startsWith('event: ')) eventName = line.slice('event: '.length).trim()
      else if (line.startsWith('data: ')) dataRaw += line.slice('data: '.length)
    }
    let data: unknown = null
    try {
      data = JSON.parse(dataRaw)
    } catch {
      return
    }

    setTurns((prev) => {
      // The assistant turn we're streaming into is always the last one.
      const last = prev[prev.length - 1]
      if (!last || last.role !== 'assistant') return prev
      const updated: ChatTurn = { ...last, tools: last.tools ? [...last.tools] : [] }

      switch (eventName) {
        case 'token': {
          const d = (data as { delta?: string }).delta ?? ''
          updated.content += d
          break
        }
        case 'tool_start': {
          const name = (data as { name?: string }).name ?? '?'
          updated.tools!.push({ name, status: 'running' })
          break
        }
        case 'tool_end': {
          const name = (data as { name?: string }).name ?? '?'
          const errMsg = (data as { error?: string }).error
          // Mark the most recent matching tool by name as resolved.
          for (let i = updated.tools!.length - 1; i >= 0; i--) {
            if (updated.tools![i].name === name && updated.tools![i].status === 'running') {
              updated.tools![i] = {
                name,
                status: errMsg ? 'error' : 'ok',
                errorMsg: errMsg,
              }
              break
            }
          }
          break
        }
        case 'error': {
          setError((data as { message?: string }).message ?? 'unknown error')
          break
        }
        case 'done': {
          const final = (data as { final?: string }).final
          if (final && !updated.content) updated.content = final
          break
        }
      }
      return [...prev.slice(0, -1), updated]
    })
  }

  return (
    <div className="fixed bottom-4 right-4 z-50 font-mono text-[12px]">
      {open ? (
        <div className="flex h-[520px] w-[380px] flex-col rounded border border-rule bg-bg-2 shadow-xl">
          {/* header */}
          <div className="flex items-center gap-2 border-b border-rule px-3 py-2">
            <span className="h-1.5 w-1.5 rounded-full bg-accent-green shadow-[0_0_8px_var(--green)]" />
            <span className="text-[12px] text-ink">Assistant</span>
            {info.model && (
              <span className="text-[10px] text-ink-3" title="active model">
                {info.model}
              </span>
            )}
            <span className="ml-auto flex items-center gap-1">
              <button
                aria-label="Clear conversation"
                title="Clear"
                onClick={clearChat}
                className="rounded px-1.5 py-0.5 text-[10px] text-ink-3 hover:bg-bg-3 hover:text-ink"
              >
                clear
              </button>
              <button
                aria-label="Close assistant"
                onClick={() => setOpen(false)}
                className="rounded px-1.5 py-0.5 text-ink-3 hover:bg-bg-3 hover:text-ink"
              >
                ×
              </button>
            </span>
          </div>

          {/* conversation */}
          <div ref={scrollRef} className="flex-1 overflow-y-auto px-3 py-2">
            {turns.length === 0 && (
              <div className="px-2 py-6 text-[12px] text-ink-3">
                Ask about your jobs, troubleshoot a failed run, or get help
                drafting a <span className="text-ink-2">cronfoundry.yaml</span>{' '}
                snippet.
                <ul className="mt-3 list-disc space-y-1 pl-4 text-[11px]">
                  <li>"Why did monday-morning fail yesterday?"</li>
                  <li>"How do I add a Slack destination?"</li>
                  <li>"What schedules are auto-paused?"</li>
                </ul>
              </div>
            )}
            {turns.map((t, i) => (
              <ChatBubble key={i} turn={t} />
            ))}
            {streaming && (
              <div className="px-2 py-1 text-[10px] text-ink-3">…thinking</div>
            )}
            {error && (
              <div className="mt-2 rounded border border-accent-red/40 bg-accent-red/10 px-2 py-1 text-[11px] text-accent-red">
                {error}
              </div>
            )}
            {needsReconnect && (
              <div className="mt-2 rounded border border-rule bg-bg-3 px-2 py-1 text-[11px] text-ink-2">
                Authentication expired. Reconnect this provider in Settings → Providers.
              </div>
            )}
          </div>

          {/* composer */}
          <div className="border-t border-rule px-2 py-2">
            <textarea
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  send()
                }
              }}
              placeholder="Ask anything…"
              rows={2}
              className="w-full resize-none rounded border border-rule-2 bg-bg-3 px-2 py-1.5 text-[12px] text-ink placeholder:text-ink-4 focus:border-ink-3 focus:outline-none"
              disabled={streaming}
            />
            <div className="mt-1 flex items-center justify-between text-[10px] text-ink-3">
              <span>Enter to send · Shift+Enter for newline</span>
              <button
                onClick={send}
                disabled={streaming || !input.trim()}
                className={cn(
                  'rounded border px-2 py-0.5 transition-colors',
                  streaming || !input.trim()
                    ? 'cursor-not-allowed border-rule-2 text-ink-4'
                    : 'border-accent-green-dim bg-accent-green-dim text-[#dffbe9] hover:bg-accent-green hover:text-[#04210f]',
                )}
              >
                send
              </button>
            </div>
          </div>
        </div>
      ) : (
        <button
          onClick={() => setOpen(true)}
          aria-label="Open assistant"
          className="flex h-11 w-11 items-center justify-center rounded-full border border-rule bg-bg-2 text-[18px] text-accent-green shadow-lg hover:border-accent-green hover:bg-bg-3"
          title="Ask the assistant"
        >
          ?
        </button>
      )}
    </div>
  )
}

function ChatBubble({ turn }: { turn: ChatTurn }) {
  const isUser = turn.role === 'user'
  return (
    <div className={cn('mb-3 flex flex-col', isUser ? 'items-end' : 'items-start')}>
      <div
        className={cn(
          'max-w-[90%] rounded px-2.5 py-1.5 text-[12px] leading-relaxed whitespace-pre-wrap break-words',
          isUser
            ? 'border border-rule-2 bg-bg-3 text-ink'
            : 'bg-bg-3/40 text-ink',
        )}
      >
        {renderContent(turn.content)}
      </div>
      {turn.tools && turn.tools.length > 0 && (
        <div className="mt-1 flex flex-wrap gap-1">
          {turn.tools.map((t, i) => (
            <span
              key={i}
              className={cn(
                'rounded border px-1.5 py-0.5 text-[10px]',
                t.status === 'running' && 'border-rule-2 text-ink-3',
                t.status === 'ok' && 'border-accent-green-dim text-accent-green',
                t.status === 'error' && 'border-accent-red/40 text-accent-red',
              )}
              title={t.errorMsg}
            >
              {t.status === 'running' ? '…' : t.status === 'ok' ? '✓' : '✕'} {t.name}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}

// renderContent splits a markdown-ish string into prose and ```yaml fenced
// blocks. We don't pull a full markdown renderer — the assistant's output
// is short and conversational, and YAML snippets are the only thing that
// really needs distinct visual treatment for copy/paste.
function renderContent(s: string): React.ReactNode {
  const parts: React.ReactNode[] = []
  const re = /```(\w+)?\n([\s\S]*?)```/g
  let lastIdx = 0
  let m: RegExpExecArray | null
  let key = 0
  while ((m = re.exec(s)) !== null) {
    if (m.index > lastIdx) {
      parts.push(<span key={key++}>{s.slice(lastIdx, m.index)}</span>)
    }
    const lang = m[1] ?? 'text'
    const code = m[2]
    parts.push(
      <pre
        key={key++}
        className="my-1 overflow-x-auto rounded bg-[#06080a] p-2 text-[11px] text-ink"
      >
        <span className="text-ink-3">{lang}</span>
        {'\n'}
        {code}
      </pre>,
    )
    lastIdx = m.index + m[0].length
  }
  if (lastIdx < s.length) parts.push(<span key={key++}>{s.slice(lastIdx)}</span>)
  return parts.length > 0 ? parts : s
}
