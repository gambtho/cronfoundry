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
}

beforeEach(() => {
  MockEventSource.instances = []
  vi.stubGlobal('EventSource', MockEventSource)
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
