import { describe, it, expect, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { Card } from './Card'

afterEach(cleanup)

describe('Card', () => {
  it('renders children', () => {
    const { getByText } = render(<Card>hello</Card>)
    expect(getByText('hello')).toBeInTheDocument()
  })

  it('renders Card.Header with label and meta', () => {
    const { getByText } = render(
      <Card>
        <Card.Header>
          Provider
          <Card.HeaderMeta>9 jobs</Card.HeaderMeta>
        </Card.Header>
        <Card.Body>body</Card.Body>
      </Card>,
    )
    expect(getByText('Provider')).toBeInTheDocument()
    expect(getByText('9 jobs')).toBeInTheDocument()
    expect(getByText('body')).toBeInTheDocument()
  })

  it('uses a section element for the outer container', () => {
    const { container } = render(<Card>x</Card>)
    expect(container.firstChild?.nodeName).toBe('SECTION')
  })
})
