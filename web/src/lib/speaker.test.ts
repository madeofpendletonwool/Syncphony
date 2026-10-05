import { describe, expect, it } from 'vitest'
import type { Playback } from './playback'
import { newer } from './playback'
import { acceptedTypes, deviceId, targetPosition } from './speaker'

const pb = (p: Partial<Playback>): Playback => ({
  roomId: 'r',
  state: 'playing',
  positionMs: 10_000,
  at: '2026-10-05T12:00:00Z',
  revision: 1,
  ...p,
})

describe('targetPosition', () => {
  const at = Date.parse('2026-10-05T12:00:00Z')
  it('runs on from the server position while playing', () => {
    expect(targetPosition(pb({}), at + 2_500)).toBe(12_500)
  })
  it('stays put while paused or loading', () => {
    expect(targetPosition(pb({ state: 'paused' }), at + 2_500)).toBe(10_000)
    expect(targetPosition(pb({ state: 'loading' }), at + 2_500)).toBe(10_000)
  })
  it('never goes backwards for a clock behind the server', () => {
    expect(targetPosition(pb({}), at - 5_000)).toBe(10_000)
  })
})

describe('acceptedTypes', () => {
  it('keeps only what the browser can play', () => {
    expect(acceptedTypes((t) => (t === 'audio/mpeg' || t === 'audio/flac' ? 'maybe' : ''))).toEqual(['audio/mpeg', 'audio/flac'])
  })
})

describe('deviceId', () => {
  it('is stable for this browser', () => {
    expect(deviceId()).toBe(deviceId())
  })
})

describe('newer', () => {
  it('prefers the higher revision', () => {
    expect(newer(pb({ revision: 3 }), pb({ revision: 2 })).revision).toBe(3)
    expect(newer(pb({ revision: 2 }), pb({ revision: 3 })).revision).toBe(3)
  })
  it('breaks ties by server time, for progress reports', () => {
    const later = pb({ positionMs: 15_000, at: '2026-10-05T12:00:05Z' })
    expect(newer(pb({}), later)).toBe(later)
    expect(newer(later, pb({}))).toBe(later)
  })
  it('ignores state from another room', () => {
    const other = pb({ roomId: 'x', revision: 0 })
    expect(newer(pb({ revision: 9 }), other)).toBe(other)
  })
})
