import { describe, it, expect, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { KV } from './KV'

afterEach(cleanup)

describe('KV', () => {
  it('renders rows with label and value', () => {
    const { getByText } = render(
      <KV>
        <KV.Row label="Region">us-east-1</KV.Row>
        <KV.Row label="Health">Reachable</KV.Row>
      </KV>,
    )
    expect(getByText('Region')).toBeInTheDocument()
    expect(getByText('us-east-1')).toBeInTheDocument()
    expect(getByText('Health')).toBeInTheDocument()
    expect(getByText('Reachable')).toBeInTheDocument()
  })

  it('uses dl/dt/dd elements for accessibility', () => {
    const { container } = render(
      <KV>
        <KV.Row label="A">1</KV.Row>
      </KV>,
    )
    expect(container.querySelector('dl')).not.toBeNull()
    expect(container.querySelector('dt')?.textContent).toBe('A')
    expect(container.querySelector('dd')?.textContent).toBe('1')
  })
})
