import { describe, expect, it } from 'vitest'
import { formatDuration, positionAt, type NowPlaying } from './now-playing'

const np = (over: Partial<NowPlaying>): NowPlaying => ({
  track: { provider: 'fake', trackId: 't', title: 'T', artists: [], durationMs: 60_000, explicit: false },
  paused: false,
  positionMs: 10_000,
  at: 1_000,
  ...over,
})

describe('positionAt', () => {
  it('extrapolates while playing and holds while paused', () => {
    expect(positionAt(np({}), 6_000)).toBe(15_000)
    expect(positionAt(np({ paused: true }), 6_000)).toBe(10_000)
  })

  it('clamps to the track', () => {
    expect(positionAt(np({}), 1_000_000)).toBe(60_000)
    expect(positionAt(np({ positionMs: -5 }), 1_000)).toBe(0)
  })
})

describe('formatDuration', () => {
  it('formats m:ss', () => {
    expect(formatDuration(0)).toBe('0:00')
    expect(formatDuration(65_400)).toBe('1:05')
    expect(formatDuration(3_600_000)).toBe('60:00')
    expect(formatDuration(undefined)).toBe('–:––')
  })
})
