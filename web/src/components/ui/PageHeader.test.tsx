import { describe, it, expect, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { PageHeader } from './PageHeader'

afterEach(cleanup)

describe('PageHeader', () => {
  it('renders the title', () => {
    const { getByRole } = render(<PageHeader title="nightly-backup" />)
    expect(getByRole('heading', { level: 1 })).toHaveTextContent('nightly-backup')
  })

  it('renders subtitle when provided', () => {
    const { getByText } = render(
      <PageHeader title="x" subtitle="postgres → s3" />,
    )
    expect(getByText('postgres → s3')).toBeInTheDocument()
  })

  it('renders eyebrow with the configured tone', () => {
    const { getByText, container } = render(
      <PageHeader
        title="x"
        eyebrow={{ tone: 'fail', text: '2 jobs failing' }}
      />,
    )
    expect(getByText('2 jobs failing')).toBeInTheDocument()
    // The eyebrow wrapper should carry the red tone class.
    expect(container.querySelector('.text-accent-red')).not.toBeNull()
  })

  it.each([
    ['ok', 'text-accent-green'],
    ['fail', 'text-accent-red'],
    ['warn', 'text-accent-amber'],
  ] as const)(
    'eyebrow tone %s applies %s',
    (tone, cls) => {
      const { container } = render(
        <PageHeader title="x" eyebrow={{ tone, text: 'hi' }} />,
      )
      expect(container.querySelector(`.${cls}`)).not.toBeNull()
    },
  )

  it('renders actions slot', () => {
    const { getByText } = render(
      <PageHeader
        title="x"
        actions={<button>Run now</button>}
      />,
    )
    expect(getByText('Run now')).toBeInTheDocument()
  })

  it('omits eyebrow when not supplied (absence-of-red is the win state)', () => {
    const { container } = render(<PageHeader title="x" />)
    expect(container.querySelector('.text-accent-red')).toBeNull()
    expect(container.querySelector('.text-accent-green')).toBeNull()
  })

  it('exposes Meta and Chip subcomponents', () => {
    const { getByText } = render(
      <PageHeader title="x">
        <PageHeader.Meta>
          <PageHeader.Chip>analytics-pipeline</PageHeader.Chip>
        </PageHeader.Meta>
      </PageHeader>,
    )
    expect(getByText('analytics-pipeline')).toBeInTheDocument()
  })
})
