import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import JobNew from './JobNew'

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
  // Re-apply skills mock after clearAllMocks resets call history (not implementation)
})

function renderJobNew() {
  return render(
    <MemoryRouter initialEntries={['/jobs/new']}>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Routes>
          <Route path="/jobs/new" element={<JobNew />} />
          <Route path="/jobs" element={<div data-testid="back-to-jobs" />} />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

async function fillRequiredFields() {
  // Wait for skills to load — the option must be present before selecting it
  expect(await screen.findByRole('option', { name: 'skills/smoke' })).toBeInTheDocument()
  const allComboboxes = screen.getAllByRole('combobox')
  const skillSelect = allComboboxes[0] // first select is skill
  await act(async () => {
    fireEvent.change(skillSelect, { target: { value: 'skills/smoke' } })
  })
  await act(async () => {
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'newjob' } })
  })
  await act(async () => {
    fireEvent.change(screen.getByPlaceholderText('0 9 * * *'), { target: { value: '0 9 * * *' } })
  })
}

describe('JobNew', () => {
  it('blocks submit when required fields missing', async () => {
    renderJobNew()
    await screen.findByRole('button', { name: /open pr/i })
    fireEvent.click(screen.getByRole('button', { name: /open pr/i }))
    expect(api.skillRepo.proposeJob).not.toHaveBeenCalled()
  })

  it('serializes the form to ProposeJobRequest and navigates back on success', async () => {
    ;(api.skillRepo.proposeJob as ReturnType<typeof vi.fn>).mockResolvedValue({
      pr_url: 'https://gh/x/y/pull/1',
      pr_number: 1,
      branch: 'b',
    })
    renderJobNew()
    await fillRequiredFields()
    // Submit the form directly to bypass any HTML5 validation issues in jsdom
    fireEvent.submit(screen.getByRole('button', { name: /open pr/i }).closest('form')!)
    await waitFor(() => expect(api.skillRepo.proposeJob).toHaveBeenCalled())
    const arg = (api.skillRepo.proposeJob as ReturnType<typeof vi.fn>).mock.calls[0][0]
    expect(arg.skill_path).toBe('skills/smoke')
    expect(arg.schedule.name).toBe('newjob')
    expect(arg.schedule.cron).toBe('0 9 * * *')
    expect(await screen.findByTestId('back-to-jobs')).toBeInTheDocument()
  })

  it('renders 412 review_url CTA on permission_required', async () => {
    const { ApiError } = await import('../lib/api-error')
    ;(api.skillRepo.proposeJob as ReturnType<typeof vi.fn>).mockRejectedValue(
      new ApiError('missing perm', 412, 'permission_required', {
        review_url: 'https://github.com/settings/apps/cf/permissions',
      }),
    )
    renderJobNew()
    await fillRequiredFields()
    fireEvent.submit(screen.getByRole('button', { name: /open pr/i }).closest('form')!)
    expect(await screen.findByText(/Review the GitHub App permissions/)).toBeInTheDocument()
  })
})
