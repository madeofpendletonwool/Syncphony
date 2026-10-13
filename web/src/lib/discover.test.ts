import { describe, expect, it } from 'vitest'
import { becauseText, fansText, feedItems, imageUrl, youtubeUrl, type DiscoveryFeed } from './discover'

const item = (key: string) => ({ key, title: key, artist: 'A', because: ['Slowdive'], seed: false })
const page = (...keys: string[]): DiscoveryFeed => ({
  items: keys.map(item),
  library: { links: 0, songs: 0, building: false },
  noGraph: false,
})

describe('becauseText', () => {
  it('names the seeds a song is near', () => {
    expect(becauseText({ because: ['Slowdive'], seed: false })).toBe('Near Slowdive')
    expect(becauseText({ because: ['Slowdive', 'Hum'], seed: false })).toBe('Near Slowdive and Hum')
    expect(becauseText({ because: ['Slowdive', 'Hum', 'Duster'], seed: false })).toBe('Near Slowdive, Hum and Duster')
  })
  it("says a seed's own song is more from them", () => {
    expect(becauseText({ because: ['Hum'], seed: true })).toBe('More from Hum')
  })
  it('says nothing without a reason', () => {
    expect(becauseText({ because: [], seed: false })).toBe('')
  })
})

describe('youtubeUrl', () => {
  it('searches YouTube for the artist and title', () => {
    expect(youtubeUrl({ artist: 'Hum', title: 'Stars & Stripes?' })).toBe(
      'https://www.youtube.com/results?search_query=Hum%20Stars%20%26%20Stripes%3F',
    )
  })
})

describe('feedItems', () => {
  it('keeps each song once, across pages', () => {
    expect(feedItems([page('a', 'b'), page('b', 'c')]).map((i) => i.key)).toEqual(['a', 'b', 'c'])
    expect(feedItems(undefined)).toEqual([])
  })
})

describe('imageUrl', () => {
  it('goes through the server', () => {
    expect(imageUrl('cover', 'abc', 120)).toBe('/api/me/discover/images/cover/abc?size=120')
    expect(imageUrl('artist', undefined)).toBeUndefined()
  })
})

describe('fansText', () => {
  it('rounds big numbers', () => {
    expect(fansText(4_101_295)).toBe('4.1M fans')
    expect(fansText(12_000_000)).toBe('12M fans')
    expect(fansText(1_000_000)).toBe('1M fans')
    expect(fansText(12_400)).toBe('12K fans')
    expect(fansText(1)).toBe('1 fan')
    expect(fansText(undefined)).toBe('')
  })
})
