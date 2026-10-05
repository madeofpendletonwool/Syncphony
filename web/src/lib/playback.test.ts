import { describe, expect, it } from 'vitest'
import { currentPosition } from '@/hooks/use-room-controls'
import { canControl, queueArtworkUrl, songsBeforeYours, toNowPlaying, type Playback, type QueueItem } from './playback'

const item = (id: string, addedBy: string, extra: Partial<QueueItem['track']> = {}): QueueItem => ({
  id,
  addedBy,
  state: 'queued',
  lanePosition: 0,
  addedAt: '',
  track: { provider: 'fake', trackId: id, title: id, artists: [], durationMs: 60_000, explicit: false, linkId: 'l', artwork: 'a', ...extra },
})

describe('songsBeforeYours', () => {
  const items = [item('a', 'ann'), item('b', 'bob'), item('c', 'me'), item('d', 'me')]
  it('counts the songs ahead of your first', () => {
    expect(songsBeforeYours(['a', 'b', 'c', 'd'], items, 'me')).toBe(2)
    expect(songsBeforeYours(['c', 'a'], items, 'me')).toBe(0)
  })
  it('is undefined when you have nothing waiting', () => {
    expect(songsBeforeYours(['a', 'b'], items, 'zed')).toBeUndefined()
  })
})

describe('canControl', () => {
  it('lets everyone control an open room', () => {
    expect(canControl({ controls: 'everyone', ownerId: 'o' }, 'x')).toBe(true)
  })
  it('limits an owner-only room to its owner', () => {
    expect(canControl({ controls: 'owner', ownerId: 'o' }, 'x')).toBe(false)
    expect(canControl({ controls: 'owner', ownerId: 'o' }, 'o')).toBe(true)
  })
})

describe('queueArtworkUrl', () => {
  it('goes through the room', () => {
    expect(queueArtworkUrl('r 1', item('i', 'me'), 100)).toBe('/api/rooms/r%201/queue/i/artwork?size=100')
  })
  it('is undefined without artwork or a link', () => {
    expect(queueArtworkUrl('r', item('i', 'me', { artwork: undefined }))).toBeUndefined()
    expect(queueArtworkUrl('r', item('i', 'me', { linkId: undefined }))).toBeUndefined()
  })
})

const playback = (p: Partial<Playback>): Playback => ({
  roomId: 'r',
  state: 'playing',
  positionMs: 10_000,
  at: '2026-10-05T12:00:00Z',
  revision: 1,
  ...p,
})

describe('toNowPlaying', () => {
  it('is null with nothing loaded', () => {
    expect(toNowPlaying('r', playback({ state: 'idle' }), [])).toBeNull()
  })
  it('maps the song, its requester and the clock', () => {
    const ann = { id: 'ann', displayName: 'Ann', color: '#7c3aed' }
    const np = toNowPlaying('r', playback({ item: item('a', 'ann'), state: 'paused' }), [ann])
    expect(np).toMatchObject({ paused: true, positionMs: 10_000, at: Date.parse('2026-10-05T12:00:00Z'), requester: ann })
    expect(np?.artworkUrl).toContain('/api/rooms/r/queue/a/artwork')
  })
})

describe('currentPosition', () => {
  const at = Date.parse('2026-10-05T12:00:00Z')
  it('advances while playing', () => {
    expect(currentPosition(playback({}), at + 5_000)).toBe(15_000)
  })
  it('holds while paused', () => {
    expect(currentPosition(playback({ state: 'paused' }), at + 5_000)).toBe(10_000)
  })
})
