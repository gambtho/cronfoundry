import { describe, it, expect, afterEach, vi } from 'vitest'
import { render, cleanup, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import ChatDock from './ChatDock'

vi.mock('../lib/api', () => ({
  api: {
    chat: {
      info: vi.fn(),
      streamURL: () => '/api/chat/stream',
    },
  },
}))

import { api } from '../lib/api'

afterEach(cleanup)

function renderDock() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <ChatDock />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('ChatDock', () => {
  it('renders nothing when chat is disabled server-side', async () => {
    vi.mocked(api.chat.info).mockResolvedValue({ enabled: false })
    const { container } = renderDock()
    // Wait a beat for the query to resolve, then assert empty.
    await waitFor(() => {
      expect(api.chat.info).toHaveBeenCalled()
    })
    expect(container.firstChild).toBeNull()
  })

  it('renders the launcher button when chat is enabled', async () => {
    vi.mocked(api.chat.info).mockResolvedValue({
      enabled: true,
      model: 'claude-sonnet-4-5',
    })
    renderDock()
    const btn = await screen.findByRole('button', { name: /open assistant/i })
    expect(btn).toBeInTheDocument()
  })
})
