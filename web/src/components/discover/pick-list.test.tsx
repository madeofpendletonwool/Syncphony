import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it } from 'vitest'
import { PickList, type Pickable } from './pick-list'

const items: Pickable[] = [
  { name: 'Low', detail: 'Starred' },
  { name: 'Slowdive', detail: '2 albums' },
  { name: 'Hum', detail: 'In this set', disabled: true },
]

function Harness() {
  const [picked, setPicked] = useState<Set<string>>(new Set(['Low']))
  return (
    <>
      <PickList label="Artists" items={items} picked={picked} onChange={setPicked} />
      <output>{[...picked].sort().join(',')}</output>
    </>
  )
}

afterEach(cleanup)

describe('PickList', () => {
  it('picks only what you check, and never one that is in already', () => {
    render(<Harness />)
    expect(screen.getByText('1 of 2 picked')).toBeTruthy()
    const low = screen.getByRole('checkbox', { name: /Low/ })
    expect(low.getAttribute('aria-checked')).toBe('true')
    fireEvent.click(screen.getByRole('checkbox', { name: /Slowdive/ }))
    expect(screen.getByRole('status').textContent).toBe('Low,Slowdive')
    expect((screen.getByRole('checkbox', { name: /Hum/ }) as HTMLButtonElement).disabled).toBe(true)

    fireEvent.click(screen.getByRole('button', { name: 'None' }))
    expect(screen.getByRole('status').textContent).toBe('')
    fireEvent.click(screen.getByRole('button', { name: 'All' }))
    expect(screen.getByRole('status').textContent).toBe('Low,Slowdive')
  })
})
