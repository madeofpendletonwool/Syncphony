import { describe, expect, it } from 'vitest'
import type { Recap } from './playlists'
import { nightRange, overlapLine, shareBars, wrappedPages } from './wrapped'

const empty: Recap = {
  stats: { plays: 3, skipped: 0, listeningMs: 600_000, topTracks: [], topArtists: [], people: [] },
  genres: [],
  decades: [],
  overlaps: [],
  playlists: [],
}

describe('wrappedPages', () => {
  it('always opens and closes', () => {
    expect(wrappedPages(empty)).toEqual(['intro', 'outro'])
  })
  it('shows only what the night has', () => {
    const r: Recap = {
      ...empty,
      topAdder: { userId: 'a', count: 4 },
      genres: [{ name: 'rock', plays: 3 }],
      overlaps: [{ userIds: ['a', 'b'], artists: ['Bowie'] }],
    }
    expect(wrappedPages(r)).toEqual(['intro', 'people', 'mix', 'overlap', 'outro'])
  })
})

describe('overlapLine', () => {
  it('lists two and counts the rest', () => {
    expect(overlapLine(['Ann', 'Bo'], ['Bowie'])).toBe('Ann and Bo matched on Bowie')
    expect(overlapLine(['Ann', 'Bo'], ['Bowie', 'Abba'])).toBe('Ann and Bo matched on Bowie and Abba')
    expect(overlapLine(['Ann', 'Bo'], ['Bowie', 'Abba', 'Queen', 'Blur'])).toBe('Ann and Bo matched on Bowie, Abba and 2 more')
  })
})

describe('shareBars', () => {
  it('scales to the biggest', () => {
    expect(shareBars([{ name: 'rock', plays: 4 }, { name: 'pop', plays: 1 }]).map((s) => s.fraction)).toEqual([1, 0.25])
  })
})

describe('nightRange', () => {
  it('reaches just past the end', () => {
    expect(nightRange({ startedAt: '2026-10-09T20:00:00.000Z', endedAt: '2026-10-09T23:00:00.000Z' })).toEqual({
      from: '2026-10-09T20:00:00.000Z',
      to: '2026-10-09T23:00:00.001Z',
    })
  })
})
