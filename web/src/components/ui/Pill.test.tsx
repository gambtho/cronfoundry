import { describe, it, expect } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'
import { Pill, pillFromRunStatus } from './Pill'

afterEach(cleanup)

describe('Pill', () => {
  it('renders the label as text', () => {
    const { getByText } = render(<Pill variant="ok">Healthy</Pill>)
    expect(getByText('Healthy')).toBeInTheDocument()
  })

  it.each([
    ['ok', 'text-accent-green'],
    ['fail', 'text-accent-red'],
    ['run', 'text-accent-amber'],
    ['skip', 'text-ink-3'],
    ['amber', 'text-accent-amber'],
    ['partial', 'text-accent-amber'],
  ] as const)(
    'applies the %s tone class',
    (variant, toneClass) => {
      const { container } = render(<Pill variant={variant}>x</Pill>)
      expect(container.firstChild).toHaveClass(toneClass)
    },
  )

  it('renders a status dot before the label', () => {
    const { container } = render(<Pill variant="ok">x</Pill>)
    // The first child of the pill is the dot span.
    const dot = container.firstChild?.firstChild as HTMLElement
    expect(dot.className).toMatch(/rounded-full/)
  })
})

describe('pillFromRunStatus', () => {
  it.each([
    ['succeeded',       'ok',      'Success'],
    ['failed',          'fail',    'Failed'],
    ['running',         'run',     'Running'],
    ['pending',         'skip',    'Pending'],
    ['partial_failure', 'partial', 'Partial'],
  ] as const)(
    'maps %s → variant=%s label=%s',
    (status, variant, label) => {
      expect(pillFromRunStatus(status)).toEqual({ variant, label })
    },
  )
})
