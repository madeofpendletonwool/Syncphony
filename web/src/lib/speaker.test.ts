import { describe, expect, it } from 'vitest'
import type { Playback } from './playback'
import { newer } from './playback'
import type { QueueItem } from './playback'
import { acceptedTypes, canSeek, deviceId, offSong, streamUrl, targetPosition } from './speaker'

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

describe('offSong', () => {
  const at = Date.parse('2026-10-05T12:00:00Z')
  const song = (id: string) => ({ id, track: { durationMs: 200_000 } }) as QueueItem
  const room = (positionMs: number) => pb({ item: song('helena'), next: song('feel-it-still'), positionMs, driver: 'stream' })
  it('is fine on the room song', () => {
    expect(offSong(room(60_000), 'helena', at)).toBe(false)
  })
  it('catches being on the next song with most of this one to go', () => {
    expect(offSong(room(60_000), 'feel-it-still', at)).toBe(true)
  })
  it('allows starting the next song in the last moments (gapless handoff)', () => {
    expect(offSong(room(195_000), 'feel-it-still', at)).toBe(false)
    expect(offSong(room(205_000), 'feel-it-still', at)).toBe(false)
  })
  it('catches any other song, or none', () => {
    expect(offSong(room(195_000), 'roxanne', at)).toBe(true)
    expect(offSong(room(60_000), undefined, at)).toBe(true)
  })
  it('leaves paused, idle and remote rooms alone', () => {
    expect(offSong({ ...room(60_000), state: 'paused' }, 'feel-it-still', at)).toBe(false)
    expect(offSong({ ...room(60_000), item: undefined, state: 'idle' }, 'feel-it-still', at)).toBe(false)
    expect(offSong({ ...room(60_000), driver: 'remote' }, 'feel-it-still', at)).toBe(false)
  })
})

describe('acceptedTypes', () => {
  it('keeps only what the browser can play', () => {
    expect(acceptedTypes((t) => (t === 'audio/mpeg' || t === 'audio/flac' ? 'maybe' : ''))).toEqual(['audio/mpeg', 'audio/flac'])
  })
})

describe('canSeek', () => {
  const ranges = (...r: [number, number][]) => ({ length: r.length, start: (i: number) => r[i][0], end: (i: number) => r[i][1] })
  it('seeks within a seekable range', () => {
    expect(canSeek(ranges([0, 200]), 95)).toBe(true)
  })
  it("can't seek a stream with no seekable range, or past what's downloaded", () => {
    expect(canSeek(ranges(), 95)).toBe(false)
    expect(canSeek(ranges([0, 12]), 95)).toBe(false)
  })
})

describe('streamUrl', () => {
  it('asks for a start only part way in', () => {
    expect(streamUrl('r 1', 'i', ['audio/mpeg', 'audio/aac'])).toBe('/api/rooms/r%201/stream/i?accept=audio%2Fmpeg%2Caudio%2Faac')
    expect(streamUrl('r', 'i', ['audio/mpeg'], 95_400.6)).toBe('/api/rooms/r/stream/i?accept=audio%2Fmpeg&start=95401')
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
