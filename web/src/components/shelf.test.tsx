import { cleanup, render } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { Shelf } from './shelf'

afterEach(cleanup)

// jsdom has no layout, so the shelf's scroll metrics are faked on the row.
function fakeMetrics(el: HTMLElement, { scrollLeft = 0, scrollWidth = 1000, clientWidth = 400 }) {
  let left = scrollLeft
  Object.defineProperty(el, 'scrollWidth', { configurable: true, value: scrollWidth })
  Object.defineProperty(el, 'clientWidth', { configurable: true, value: clientWidth })
  Object.defineProperty(el, 'scrollLeft', {
    configurable: true,
    get: () => left,
    set: (v: number) => {
      left = v
    },
  })
  return () => left
}

function wheel(el: HTMLElement, init: WheelEventInit) {
  const event = new WheelEvent('wheel', { cancelable: true, ...init })
  el.dispatchEvent(event)
  return event
}

describe('Shelf', () => {
  it('turns a vertical mouse wheel into sideways scrolling', () => {
    const { container } = render(
      <Shelf>
        <span>one</span>
        <span>two</span>
      </Shelf>,
    )
    const left = fakeMetrics(container.firstElementChild as HTMLElement, {})
    const event = wheel(container.firstElementChild as HTMLElement, { deltaY: 120 })
    expect(event.defaultPrevented).toBe(true)
    expect(left()).toBe(120)
  })

  it('scales line-mode wheel deltas like Firefox reports them', () => {
    const { container } = render(<Shelf>playlists</Shelf>)
    const el = container.firstElementChild as HTMLElement
    const left = fakeMetrics(el, {})
    const event = wheel(el, { deltaY: 3, deltaMode: WheelEvent.DOM_DELTA_LINE })
    expect(event.defaultPrevented).toBe(true)
    expect(left()).toBe(99)
  })

  it('gives the wheel back to the page at both ends of the row', () => {
    const { container } = render(<Shelf>playlists</Shelf>)
    const el = container.firstElementChild as HTMLElement
    fakeMetrics(el, { scrollLeft: 600, scrollWidth: 1000, clientWidth: 400 })
    expect(wheel(el, { deltaY: 120 }).defaultPrevented).toBe(false)

    fakeMetrics(el, { scrollLeft: 0 })
    expect(wheel(el, { deltaY: -120 }).defaultPrevented).toBe(false)
  })

  it('never claims the wheel when the row already fits', () => {
    const { container } = render(<Shelf>playlists</Shelf>)
    const el = container.firstElementChild as HTMLElement
    fakeMetrics(el, { scrollWidth: 400, clientWidth: 400 })
    expect(wheel(el, { deltaY: 120 }).defaultPrevented).toBe(false)
  })

  it('leaves trackpad sideways swipes and pinch-zoom to the browser', () => {
    const { container } = render(<Shelf>playlists</Shelf>)
    const el = container.firstElementChild as HTMLElement
    const left = fakeMetrics(el, {})
    expect(wheel(el, { deltaX: 80, deltaY: 0 }).defaultPrevented).toBe(false)
    expect(wheel(el, { deltaY: 120, ctrlKey: true }).defaultPrevented).toBe(false)
    expect(left()).toBe(0)
  })
})
