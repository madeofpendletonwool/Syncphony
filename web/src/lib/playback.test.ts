import { describe, expect, it } from 'vitest'
import { currentPosition } from '@/hooks/use-room-controls'
import { can, queueArtworkUrl, skipMode, songsBeforeYours, toNowPlaying, type Playback, type QueueItem } from './playback'

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
  it("doesn't count autopilot's songs as yours", () => {
    const auto = { ...item('e', 'zed'), autopilot: { seedTitle: 'Heroes' } }
    expect(songsBeforeYours(['e'], [...items, auto], 'zed')).toBeUndefined()
  })
})

describe('permissions', () => {
  const room = (skip: 'everyone' | 'vote' | 'owner', rest: 'everyone' | 'owner' = 'everyone') => ({
    ownerId: 'o',
    permissions: { playPause: rest, seek: 'owner' as const, skip, speaker: rest },
  })
  it('checks each permission on its own', () => {
    expect(can(room('owner'), 'x', 'playPause')).toBe(true)
    expect(can(room('owner'), 'x', 'seek')).toBe(false)
    expect(can(room('owner', 'owner'), 'x', 'speaker')).toBe(false)
  })
  it('lets the owner do anything', () => {
    expect(can(room('owner', 'owner'), 'o', 'seek')).toBe(true)
    expect(can(room('vote'), 'o', 'skip')).toBe(true)
  })
  it('works out how you can skip', () => {
    const bobs = { addedBy: 'bob' }
    expect(skipMode(room('everyone'), 'x', bobs)).toBe('skip')
    expect(skipMode(room('vote'), 'x', bobs)).toBe('vote')
    expect(skipMode(room('owner'), 'x', bobs)).toBeUndefined()
    expect(skipMode(room('owner'), 'bob', bobs)).toBe('skip')
    expect(skipMode(room('vote'), 'bob', bobs)).toBe('skip')
    expect(skipMode(room('vote'), 'o', bobs)).toBe('skip')
  })
  it("leaves autopilot's songs to the room, even for whose taste seeded them", () => {
    const auto = { addedBy: 'bob', autopilot: {} }
    expect(skipMode(room('vote'), 'bob', auto)).toBe('vote')
    expect(skipMode(room('everyone'), 'bob', auto)).toBe('skip')
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
  it("credits autopilot's songs to autopilot, not the seed's member", () => {
    const ann = { id: 'ann', displayName: 'Ann', color: '#7c3aed' }
    const np = toNowPlaying('r', playback({ item: { ...item('a', 'ann'), autopilot: { seedTitle: 'Heroes' } }, state: 'playing' }), [ann])
    expect(np?.requester).toBeUndefined()
    expect(np?.autopilot).toEqual({ seedTitle: 'Heroes' })
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
