import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import JobImport from './JobImport'

vi.mock('../lib/api', () => ({
  api: {
    skills: {
      list: vi.fn().mockResolvedValue([
        {
          id: '1',
          path: 'skills/smoke',
          name: 'smoke',
          repo_id: 'r',
          current_sha: 'abc',
          updated_at: '2026-05-03T00:00:00Z',
          owner: 'o',
          repo_name: 'r',
        },
      ]),
    },
    skillRepo: { proposeJob: vi.fn() },
  },
}))
const { api } = await import('../lib/api')

beforeEach(() => {
  vi.clearAllMocks()
})

function renderImport() {
  return render(
    <MemoryRouter initialEntries={['/jobs/import']}>
      <QueryClientProvider client={new QueryClient()}>
        <Routes>
          <Route path="/jobs/import" element={<JobImport />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

describe('JobImport', () => {
  it('shows YAML parse error inline', async () => {
    renderImport()
    await screen.findByRole('option', { name: 'skills/smoke' })
    fireEvent.change(screen.getByLabelText(/skill/i), { target: { value: 'skills/smoke' } })
    fireEvent.change(screen.getByLabelText(/schedule yaml/i), {
      target: { value: 'name: x\n  : :: badly indented' },
    })
    fireEvent.click(screen.getByRole('button', { name: /open pr/i }))
    expect(await screen.findByText(/^YAML:/)).toBeInTheDocument()
    expect(api.skillRepo.proposeJob).not.toHaveBeenCalled()
  })

  it('parses valid YAML and submits the same shape as JobNew', async () => {
    ;(api.skillRepo.proposeJob as any).mockResolvedValue({
      pr_url: 'u',
      pr_number: 1,
      branch: 'b',
    })
    renderImport()
    await screen.findByRole('option', { name: 'skills/smoke' })
    fireEvent.change(screen.getByLabelText(/skill/i), { target: { value: 'skills/smoke' } })
    fireEvent.change(screen.getByLabelText(/schedule yaml/i), {
      target: {
        value:
          'name: hourly\ncron: "0 * * * *"\ntimezone: UTC\nprovider: copilot-enterprise\nmodel: gpt-5-mini\n',
      },
    })
    fireEvent.click(screen.getByRole('button', { name: /open pr/i }))
    await waitFor(() => expect(api.skillRepo.proposeJob).toHaveBeenCalled())
    const arg = (api.skillRepo.proposeJob as any).mock.calls[0][0]
    expect(arg.skill_path).toBe('skills/smoke')
    expect(arg.schedule.name).toBe('hourly')
    expect(arg.schedule.cron).toBe('0 * * * *')
  })
})
