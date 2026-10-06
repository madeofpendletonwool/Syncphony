import { afterEach, describe, expect, it } from 'vitest'
import { serverNow, serverOffset, syncServerClock, toLocalTime } from './clock'

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

  it('ignores garbage', () => {
    syncServerClock(new Date(10_000).toISOString(), 4_000)
    syncServerClock('not a time')
    expect(serverOffset()).toBe(6_000)
  })
})
