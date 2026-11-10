import { afterEach, describe, expect, it } from 'vitest'
import { offsetFrom, serverNow, serverOffset, syncServerClock, toLocalTime } from './clock'

describe('server clock', () => {
  afterEach(() => syncServerClock(new Date(1000).toISOString(), 1000))

  it('keeps the offset of a clock that is off', () => {
    syncServerClock(new Date(10_000).toISOString(), 4_000)
    expect(serverOffset()).toBe(6_000)
    expect(serverNow(4_000)).toBe(10_000)
    expect(toLocalTime(10_000)).toBe(4_000)
  })

  it('ignores latency-sized differences', () => {
    syncServerClock(new Date(10_000).toISOString(), 10_120)
    expect(serverOffset()).toBe(0)
  })

  it('measures the offset from the quickest round trip', () => {
    expect(
      offsetFrom([
        // Slow, and the answer came early in it: misleading.
        { sent: 1000, received: 1400, server: 1100 },
        // Quick: the server's 2120 was about our 2020.
        { sent: 2000, received: 2040, server: 2120 },
        { sent: 3000, received: 3200, server: 3300 },
      ]),
    ).toBe(100)
    expect(offsetFrom([])).toBeNull()
  })

  it('ignores garbage', () => {
    syncServerClock(new Date(10_000).toISOString(), 4_000)
    syncServerClock('not a time')
    expect(serverOffset()).toBe(6_000)
  })
})
