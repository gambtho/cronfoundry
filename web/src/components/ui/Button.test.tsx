import { describe, it, expect, afterEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { Button, IconButton } from './Button'

afterEach(cleanup)

describe('Button', () => {
  it('renders children', () => {
    const { getByRole } = render(<Button>Run now</Button>)
    expect(getByRole('button')).toHaveTextContent('Run now')
  })

  it('uses the primary variant classes when variant="primary"', () => {
    const { getByRole } = render(<Button variant="primary">Go</Button>)
    expect(getByRole('button').className).toMatch(/bg-accent-green-dim/)
  })

  it('uses the ghost variant classes when variant="ghost"', () => {
    const { getByRole } = render(<Button variant="ghost">Cancel</Button>)
    expect(getByRole('button').className).toMatch(/bg-transparent/)
  })

  it('renders the keyboard shortcut hint', () => {
    const { getByText } = render(<Button shortcut="R">Run now</Button>)
    expect(getByText('R')).toBeInTheDocument()
  })

  it('forwards onClick', () => {
    let clicked = false
    const { getByRole } = render(
      <Button onClick={() => (clicked = true)}>Click</Button>,
    )
    fireEvent.click(getByRole('button'))
    expect(clicked).toBe(true)
  })

  it('respects disabled', () => {
    const { getByRole } = render(<Button disabled>x</Button>)
    expect(getByRole('button')).toBeDisabled()
  })
})

describe('IconButton', () => {
  it('renders an icon as children', () => {
    const { getByRole } = render(<IconButton aria-label="Run">▶</IconButton>)
    expect(getByRole('button', { name: 'Run' })).toHaveTextContent('▶')
  })
})
