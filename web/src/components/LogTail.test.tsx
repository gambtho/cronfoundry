import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import LogTail from './LogTail'

class MockEventSource {
  static instances: MockEventSource[] = []
  url: string
  onmessage: ((ev: MessageEvent) => void) | null = null
  onerror: ((ev: Event) => void) | null = null
  listeners = new Map<string, (ev: MessageEvent) => void>()
  closed = false

  constructor(url: string) {
    this.url = url
    MockEventSource.instances.push(this)
  }
  addEventListener(type: string, fn: (ev: MessageEvent) => void) {
    this.listeners.set(type, fn)
  }
  close() {
    this.closed = true
  }
  emit(data: unknown) {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(data) }))
  }
  emitDone() {
    this.listeners.get('done')?.(new MessageEvent('done', { data: '{}' }))
  }
  emitError() {
    this.onerror?.(new Event('error'))
  }
}

beforeEach(() => {
  MockEventSource.instances = []
  vi.stubGlobal('EventSource', MockEventSource)
  // Default fetch stub: returns empty event list so incidental api.runs.events()
  // calls in non-static-mode tests don't produce unhandled rejections.
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true,
    status: 200,
    json: async () => [],
  })))
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('LogTail — streaming mode', () => {
  it('opens an EventSource to the stream URL when status is running', () => {
    render(<LogTail runId="abc" status="running" />)
    expect(MockEventSource.instances).toHaveLength(1)
    expect(MockEventSource.instances[0].url).toBe('/api/runs/abc/events/stream')
  })

  it('closes the EventSource on unmount', () => {
    const { unmount } = render(<LogTail runId="abc" status="running" />)
    const es = MockEventSource.instances[0]
    unmount()
    expect(es.closed).toBe(true)
  })

  it('does NOT open a stream when status is terminal on mount', () => {
    render(<LogTail runId="abc" status="succeeded" />)
    expect(MockEventSource.instances).toHaveLength(0)
  })

  it('closes the stream when status transitions to terminal', () => {
    const { rerender } = render(<LogTail runId="abc" status="running" />)
    const es = MockEventSource.instances[0]
    expect(es.closed).toBe(false)
    rerender(<LogTail runId="abc" status="succeeded" />)
    expect(es.closed).toBe(true)
  })
})

describe('LogTail — stream termination', () => {
  it("closes when server emits 'done' event", () => {
    render(<LogTail runId="abc" status="running" />)
    const es = MockEventSource.instances[0]
    es.emitDone()
    expect(es.closed).toBe(true)
  })
})

describe('LogTail — static mode', () => {
  it('fetches historical events when status is terminal on mount', async () => {
    const fetchSpy = vi.fn(async () => [
      { id: 1, run_id: 'abc', ts: new Date().toISOString(), level: 'info', event_type: 'llm.start', payload_json: {} },
      { id: 2, run_id: 'abc', ts: new Date().toISOString(), level: 'info', event_type: 'publish.slack.ok', payload_json: {} },
    ])
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      json: fetchSpy,
    })))

    const { findByText } = render(<LogTail runId="abc" status="succeeded" />)
    await findByText('llm.start')
    await findByText('publish.slack.ok')
    expect(MockEventSource.instances).toHaveLength(0)
  })
})

describe('LogTail — reconnect cap', () => {
  it('shows "connection lost" after 5 consecutive errors', async () => {
    const { findByText, queryByText } = render(
      <LogTail runId="abc" status="running" />
    )
    const es = MockEventSource.instances[0]

    for (let i = 0; i < 4; i++) es.emitError()
    expect(queryByText(/connection lost/i)).toBeNull()
    expect(es.closed).toBe(false)

    es.emitError() // 5th
    await findByText(/connection lost/i)
    expect(es.closed).toBe(true)
  })
})

describe('LogTail — auto-scroll', () => {
  it('scrolls to bottom on new event when sticky', () => {
    const scrollToSpy = vi.fn()
    // jsdom: override Element.prototype so our ref's scrollTo is captured
    Object.defineProperty(HTMLElement.prototype, 'scrollTo', {
      value: scrollToSpy,
      configurable: true,
    })

    render(<LogTail runId="abc" status="running" />)
    const es = MockEventSource.instances[0]
    es.emit({
      id: 10,
      run_id: 'abc',
      ts: new Date().toISOString(),
      level: 'info',
      event_type: 'llm.chunk',
      payload_json: {},
    })
    // One frame later React has flushed and the effect has run
    return Promise.resolve().then(() => {
      expect(scrollToSpy).toHaveBeenCalled()
    })
  })
})
