import { describe, expect, it } from 'vitest'
import { artworkUrl, interleave, totalDuration, trackKey, type SearchGroup } from './browse'

const group = (linkId: string, titles: string[]): SearchGroup => ({
  linkId,
  ownerId: 'me',
  provider: 'fake',
  accountLabel: linkId,
  tracks: titles.map((title) => ({
    linkId,
    provider: 'fake',
    trackId: title,
    title,
    artists: [],
    durationMs: 1000,
    explicit: false,
  })),
  albums: [],
  artists: [],
})

describe('interleave', () => {
  it('takes turns between links and keeps each item’s link', () => {
    const out = interleave([group('a', ['a1', 'a2', 'a3']), group('b', ['b1'])], (g) => g.tracks)
    expect(out.map((t) => t.title)).toEqual(['a1', 'b1', 'a2', 'a3'])
    expect(out[1].linkId).toBe('b')
  })
  it('handles no groups', () => {
    expect(interleave([], (g) => g.tracks)).toEqual([])
  })
})

describe('artworkUrl', () => {
  it('escapes the ref and link', () => {
    expect(artworkUrl('l 1', 'al-1&x', 200)).toBe('/api/links/l%201/artwork?ref=al-1%26x&size=200')
  })
  it('is undefined without a ref', () => {
    expect(artworkUrl('l', undefined)).toBeUndefined()
  })
})

describe('totalDuration', () => {
  it.each([
    [[180_000, 200_000], '6 min'],
    [[3_600_000, 300_000], '1 hr 5 min'],
  ])('%j → %s', (ms, want) => {
    expect(totalDuration(ms.map((durationMs) => ({ durationMs })))).toBe(want)
  })
})

it('trackKey separates the same song on two links', () => {
  expect(trackKey({ linkId: 'a', trackId: '1' })).not.toBe(trackKey({ linkId: 'b', trackId: '1' }))
})
