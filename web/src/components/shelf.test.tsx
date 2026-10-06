import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Shelf } from './shelf'

afterEach(cleanup)

// jsdom has no layout, so the row's scroll metrics are faked, then a scroll
// event makes the shelf measure them.
function renderShelf({ scrollLeft = 0, scrollWidth = 1000, clientWidth = 400 }) {
  const { container } = render(
    <Shelf>
      <span>one</span>
      <span>two</span>
    </Shelf>,
  )
  const row = container.querySelector('.shelf-scrollbar') as HTMLElement
  Object.defineProperty(row, 'scrollWidth', { configurable: true, value: scrollWidth })
  Object.defineProperty(row, 'clientWidth', { configurable: true, value: clientWidth })
  row.scrollLeft = scrollLeft
  row.scrollBy = vi.fn()
  fireEvent.scroll(row)
  return row
}

const back = () => screen.queryByRole('button', { name: 'Scroll back' })
const forward = () => screen.queryByRole('button', { name: 'Scroll forward' })

describe('Shelf', () => {
  it('shows no arrows when the row already fits', () => {
    renderShelf({ scrollWidth: 400, clientWidth: 400 })
    expect(back()).toBeNull()
    expect(forward()).toBeNull()
  })

  it('only offers to scroll forward at the start of the row', () => {
    renderShelf({})
    expect(back()).toBeNull()
    expect(forward()).not.toBeNull()
  })

  it('offers both directions partway along', () => {
    renderShelf({ scrollLeft: 300 })
    expect(back()).not.toBeNull()
    expect(forward()).not.toBeNull()
  })

  it('only offers to scroll back at the end of the row', () => {
    renderShelf({ scrollLeft: 600 })
    expect(back()).not.toBeNull()
    expect(forward()).toBeNull()
  })

  it('pages most of a screen at a time', () => {
    const row = renderShelf({ scrollLeft: 300 })
    fireEvent.click(forward()!)
    expect(row.scrollBy).toHaveBeenLastCalledWith(expect.objectContaining({ left: 320 }))
    fireEvent.click(back()!)
    expect(row.scrollBy).toHaveBeenLastCalledWith(expect.objectContaining({ left: -320 }))
  })
})
