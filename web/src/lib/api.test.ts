import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'

describe('apiFetch CSRF behavior', () => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let fetchSpy: any

  beforeEach(() => {
    Object.defineProperty(document, 'cookie', {
      configurable: true,
      get: () => 'cf_csrf=tok123; cf_session=sig.payload',
    })
    fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('null', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.resetModules()
  })

  it('sets X-CSRF-Token on POST', async () => {
    const { api } = await import('./api')
    await api.repos.connect({ install_id: 1, owner: 'a', name: 'b' })
    const init = fetchSpy.mock.calls[0][1] as RequestInit
    const headers = new Headers(init.headers)
    expect(headers.get('X-CSRF-Token')).toBe('tok123')
  })

  it('omits X-CSRF-Token on GET', async () => {
    const { api } = await import('./api')
    await api.repos.list()
    const init = fetchSpy.mock.calls[0][1] as RequestInit
    const headers = new Headers(init?.headers)
    expect(headers.get('X-CSRF-Token')).toBeNull()
  })

  it('redirects to /oauth/login on 403 csrf response', async () => {
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'csrf cookie missing' }), {
        status: 403,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    const originalLocation = window.location
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    delete (window as any).location
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(window as any).location = { href: '' }

    const { api } = await import('./api')
    await expect(api.repos.connect({ install_id: 1, owner: 'a', name: 'b' })).rejects.toThrow(
      /csrf rejected/,
    )
    expect(window.location.href).toBe('/oauth/login')

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(window as any).location = originalLocation
  })
})
