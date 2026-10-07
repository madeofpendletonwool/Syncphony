import { describe, expect, it } from 'vitest'
import { queuedCopy, sameSong } from './duplicates'
import type { QueueItem } from './playback'

const track = (provider: string, trackId: string, title: string, artist: string) => ({ provider, trackId, title, artists: [artist] })
const item = (id: string, state: string, t: ReturnType<typeof track>) => ({ id, state, track: t }) as QueueItem

describe('sameSong', () => {
  it('matches the same track', () => {
    expect(sameSong(track('spotify', '1', 'Heroes', 'David Bowie'), track('spotify', '1', 'x', 'y'))).toBe(true)
  })
  it('matches a song across services, loosely', () => {
    expect(sameSong(track('spotify', '1', '“Heroes” - 2017 Remaster', 'David Bowie'), track('navidrome', 'a', 'Heroes', 'david bowie'))).toBe(true)
    expect(sameSong(track('spotify', '1', 'Café (Live)', 'Ana'), track('navidrome', 'a', 'Cafe', 'Ana'))).toBe(true)
  })
  it("doesn't match different songs", () => {
    expect(sameSong(track('spotify', '1', 'Heroes', 'David Bowie'), track('navidrome', 'a', 'Heroes', 'Peter Gabriel'))).toBe(false)
    expect(sameSong(track('spotify', '1', 'Heroes', 'David Bowie'), track('spotify', '2', 'Changes', 'David Bowie'))).toBe(false)
  })
})

describe('queuedCopy', () => {
  const heroes = track('spotify', '1', 'Heroes', 'David Bowie')
  it('finds a waiting or playing copy', () => {
    expect(queuedCopy([item('a', 'played', heroes), item('b', 'queued', heroes)], heroes)?.id).toBe('b')
    expect(queuedCopy([item('a', 'playing', heroes)], heroes)?.id).toBe('a')
  })
  it('ignores songs that already played or were removed', () => {
    expect(queuedCopy([item('a', 'played', heroes), item('b', 'removed', heroes)], heroes)).toBeUndefined()
  })
})
