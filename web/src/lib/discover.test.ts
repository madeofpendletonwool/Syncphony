import { describe, expect, it } from 'vitest'
import {
  becauseText,
  fansText,
  feedItems,
  groupWants,
  imageUrl,
  kindText,
  landedText,
  mixFresh,
  releaseCoverUrl,
  releaseDateText,
  releaseYoutubeUrl,
  starredText,
  yieldText,
  youtubeUrl,
  type DiscoveryFeed,
  type DiscoveryRelease,
  type WantItem,
} from './discover'

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

const release = (key: string): DiscoveryRelease => ({
  key,
  title: key,
  artist: 'A',
  date: '2026-10-01',
  kind: 'album',
  sources: ['deezer'],
  because: ['Slowdive'],
  seed: false,
})

describe('mixFresh', () => {
  const songs = ['a', 'b', 'c', 'd', 'e', 'f'].map(item)
  const order = (entries: ReturnType<typeof mixFresh>) => entries.map((e) => (e.kind === 'song' ? e.item.key : `R${e.release.key}`))
  it('puts a release after the first song, then one every few', () => {
    expect(order(mixFresh(songs, ['1', '2', '3'].map(release), false, 4))).toEqual(['a', 'R1', 'b', 'c', 'd', 'e', 'R2', 'f'])
  })
  it('keeps what is left over for the end of the feed', () => {
    expect(order(mixFresh(songs.slice(0, 2), ['1', '2'].map(release), true))).toEqual(['a', 'R1', 'b', 'R2'])
    expect(order(mixFresh([], ['1'].map(release), true))).toEqual(['R1'])
  })
  it('is just the songs without releases', () => {
    expect(order(mixFresh(songs.slice(0, 2), [], true))).toEqual(['a', 'b'])
  })
})

describe('releaseDateText', () => {
  const now = new Date(2026, 9, 13, 15).getTime()
  it('says how lately it came out', () => {
    expect(releaseDateText('2026-10-13', now)).toBe('Out today')
    expect(releaseDateText('2026-10-12', now)).toBe('Out yesterday')
    expect(releaseDateText('2026-10-09', now)).toBe('Out 4 days ago')
    expect(releaseDateText('2026-09-19', now)).toMatch(/^Out .*19/)
  })
  it('names the month when that is all that is known', () => {
    expect(releaseDateText('2026-09', now)).toMatch(/^Out in /)
    expect(releaseDateText('', now)).toBe('')
  })
})

describe('releaseCoverUrl', () => {
  it("uses Deezer's cover, else the Cover Art Archive's", () => {
    expect(releaseCoverUrl({ cover: 'abc' }, 120)).toBe('/api/me/discover/images/cover/abc?size=120')
    expect(releaseCoverUrl({ coverRelease: 'r1', mbid: 'g1' })).toBe('/api/me/discover/release-art/r1?size=250')
    expect(releaseCoverUrl({ mbid: 'g1' })).toBe('/api/me/discover/release-art/g1?group=true&size=250')
    expect(releaseCoverUrl({})).toBeUndefined()
  })
})

describe('releaseYoutubeUrl', () => {
  it('searches for the whole album, or the single', () => {
    expect(releaseYoutubeUrl({ artist: 'Hum', title: 'Inlet', kind: 'album' })).toBe(
      'https://www.youtube.com/results?search_query=Hum%20Inlet%20full%20album',
    )
    expect(releaseYoutubeUrl({ artist: 'Hum', title: 'Waves', kind: 'single' })).toBe('https://www.youtube.com/results?search_query=Hum%20Waves')
  })
})

describe('kindText', () => {
  it('names kinds of release', () => {
    expect(kindText('ep')).toBe('EP')
    expect(kindText('live')).toBe('Live album')
  })
})

describe('the Want list', () => {
  const want = (id: string, artist: string, status: WantItem['status']): WantItem => ({
    id,
    kind: 'track',
    artist,
    title: id,
    status,
    addedAt: '2026-10-14T00:00:00Z',
  })

  it('groups by artist, A to Z, each keeping its order', () => {
    const items = [want('a', 'slowdive', 'wanted'), want('b', 'Hum', 'wanted'), want('c', 'Slowdive', 'wanted'), want('d', 'Hum', 'got')]
    const groups = groupWants(items, 'wanted')
    expect(groups.map((g) => g.artist)).toEqual(['Hum', 'slowdive'])
    expect(groups[1].items.map((w) => w.id)).toEqual(['a', 'c'])
    expect(groupWants(items, 'got').map((g) => g.items.length)).toEqual([1])
    expect(groupWants(items, 'dismissed')).toEqual([])
  })

  it('says what landed and what a set yielded', () => {
    expect(landedText(1)).toBe('1 wanted thing landed in your library')
    expect(landedText(3)).toBe('3 wanted things landed in your library')
    expect(yieldText({ wanted: 0, got: 0 })).toBe('')
    expect(yieldText({ wanted: 5, got: 2 })).toBe('2 of 5 wanted got')
  })

  it('says what of an artist you starred', () => {
    expect(starredText({ name: 'Low', artist: true, albums: 0, songs: 0 })).toBe('Starred')
    expect(starredText({ name: 'Slowdive', artist: false, albums: 2, songs: 1 })).toBe('2 albums · 1 song')
  })
})
