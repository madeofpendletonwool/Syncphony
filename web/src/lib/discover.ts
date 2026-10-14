import { infiniteQueryOptions, queryOptions, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { createStore, useStore } from './store'

// Personal discovery (ADR 0017): seed sets of artists you pick, each a
// feed of music you don't have yet. Yours alone, outside any room.

export type DiscoverySet = components['schemas']['DiscoverySet']
export type DiscoverySetSummary = components['schemas']['DiscoverySetSummary']
export type DiscoveryItem = components['schemas']['DiscoveryItem']
export type DiscoveryFeed = components['schemas']['DiscoveryFeed']
export type DiscoveryMode = components['schemas']['DiscoveryMode']
export type DiscoveryArtist = components['schemas']['DiscoveryArtist']
export type DiscoveryLibrary = components['schemas']['DiscoveryLibrary']
export type DiscoveryMap = components['schemas']['DiscoveryMap']
export type DiscoveryMapNode = components['schemas']['DiscoveryMapNode']
export type DiscoveryMapEdge = components['schemas']['DiscoveryMapEdge']
export type DiscoveryRelease = components['schemas']['DiscoveryRelease']
export type DiscoveryFresh = components['schemas']['DiscoveryFresh']
export type DiscoveryFreshDays = components['schemas']['DiscoveryFreshDays']
export type DiscoveryRoomWeight = components['schemas']['DiscoveryRoomWeight']
export type DiscoverySuggestions = components['schemas']['DiscoverySuggestions']
export type DiscoveryOffer = components['schemas']['DiscoveryOffer']
export type DiscoveryStarredArtist = components['schemas']['DiscoveryStarredArtist']
export type DiscoveryTasteArtist = components['schemas']['DiscoveryTasteArtist']
export type DiscoveryThumbResult = components['schemas']['DiscoveryThumbResult']
export type WantItem = components['schemas']['WantItem']
export type WantList = components['schemas']['WantList']
export type WantStatus = components['schemas']['WantStatus']

/** How far back a Fresh feed can look. */
export const freshWindows: DiscoveryFreshDays[] = [30, 60, 90]

export const setsQuery = queryOptions({
  queryKey: ['discover', 'sets'],
  queryFn: () => unwrap(api.GET('/me/discover/sets')),
})

export const setQuery = (id: string) =>
  queryOptions({
    queryKey: ['discover', 'sets', id],
    queryFn: () => unwrap(api.GET('/me/discover/sets/{setId}', { params: { path: { setId: id } } })),
  })

export const feedQuery = (setId: string) =>
  infiniteQueryOptions({
    queryKey: ['discover', 'feed', setId],
    queryFn: ({ pageParam }) => fetchFeed(setId, pageParam),
    initialPageParam: '',
    getNextPageParam: (page) => page.next,
    staleTime: 10 * 60_000,
  })

function fetchFeed(setId: string, cursor: string, refresh = false) {
  return unwrap(
    api.GET('/me/discover/sets/{setId}/feed', {
      params: { path: { setId }, query: { ...(cursor ? { cursor } : {}), ...(refresh ? { refresh } : {}) } },
    }),
  )
}

/** Builds a set's feed again now, and starts it over from its first page. */
export async function refreshFeed(qc: QueryClient, setId: string) {
  const first = await fetchFeed(setId, '', true)
  qc.setQueryData(feedQuery(setId).queryKey, { pages: [first], pageParams: [''] })
}

export const freshQuery = (setId: string) =>
  queryOptions({
    queryKey: ['discover', 'fresh', setId],
    queryFn: ({ signal }) => unwrap(api.GET('/me/discover/sets/{setId}/fresh', { params: { path: { setId } }, signal })),
    staleTime: 10 * 60_000,
    // Some artists' releases are being looked up: look again a few
    // times, and the feed fills out.
    refetchInterval: (q) => (q.state.data?.partial && q.state.dataUpdateCount < 4 ? 8_000 : false),
  })

export const releaseTracksQuery = (r: Pick<DiscoveryRelease, 'artist' | 'title' | 'deezerId'>) =>
  queryOptions({
    queryKey: ['discover', 'release-tracks', r.artist, r.title, r.deezerId ?? 0],
    queryFn: ({ signal }) =>
      unwrap(
        api.GET('/me/discover/release-tracks', {
          params: { query: { artist: r.artist, title: r.title, ...(r.deezerId ? { deezerId: r.deezerId } : {}) } },
          signal,
        }),
      ),
    staleTime: 60 * 60_000,
  })

export const blocksQuery = queryOptions({
  queryKey: ['discover', 'blocks'],
  queryFn: () => unwrap(api.GET('/me/discover/blocks')),
})

export const libraryQuery = queryOptions({
  queryKey: ['discover', 'library'],
  queryFn: () => unwrap(api.GET('/me/discover/library')),
  // While it's indexing, look again now and then.
  refetchInterval: (q) => (q.state.data?.building ? 5_000 : false),
})

export const artistSearchQuery = (q: string) =>
  queryOptions({
    queryKey: ['discover', 'artists', q],
    queryFn: ({ signal }) => unwrap(api.GET('/me/discover/artists', { params: { query: { q } }, signal })),
    enabled: q.trim().length > 0,
    staleTime: 5 * 60_000,
  })

/** What a map is of: one of your sets, or any artist. */
export type MapOf = { setId: string } | { artist: string }

export const mapQuery = (of: MapOf) =>
  queryOptions({
    queryKey: ['discover', 'map', of],
    queryFn: ({ signal }) => unwrap(api.GET('/me/discover/map', { params: { query: of }, signal })),
    staleTime: 5 * 60_000,
    // Artists the server didn't know yet are being looked up: look again
    // a few times, and the map fills out.
    refetchInterval: (q) => (q.state.data?.partial && q.state.dataUpdateCount < 4 ? 6_000 : false),
  })

export const artistSongsQuery = (artist: string) =>
  queryOptions({
    queryKey: ['discover', 'songs', artist],
    queryFn: ({ signal }) => unwrap(api.GET('/me/discover/songs', { params: { query: { artist } }, signal })),
    staleTime: 5 * 60_000,
  })

/** Puts a changed set in the cache, refreshes the list, and drops its stale feed. */
export function setChanged(qc: QueryClient, set: DiscoverySet) {
  qc.setQueryData(setQuery(set.id).queryKey, set)
  void qc.invalidateQueries({ queryKey: setsQuery.queryKey, exact: true })
  void qc.resetQueries({ queryKey: feedQuery(set.id).queryKey })
  void qc.invalidateQueries({ queryKey: mapQuery({ setId: set.id }).queryKey })
  void qc.invalidateQueries({ queryKey: freshQuery(set.id).queryKey })
  void qc.invalidateQueries({ queryKey: ['discover', 'suggestions', set.id] })
}

export function createSet(name: string, artists: { name: string; mbid?: string }[] = []) {
  return unwrap(api.POST('/me/discover/sets', { body: { name, artists } }))
}

export function updateSet(
  id: string,
  body: {
    name?: string
    explore?: number
    mode?: DiscoveryMode
    freshDays?: DiscoveryFreshDays
    growAuto?: boolean
    roomId?: string
    roomWeight?: DiscoveryRoomWeight
  },
) {
  return unwrap(api.PATCH('/me/discover/sets/{setId}', { params: { path: { setId: id } }, body }))
}

export function deleteSet(id: string) {
  return unwrap(api.DELETE('/me/discover/sets/{setId}', { params: { path: { setId: id } } }))
}

export function addArtist(id: string, name: string) {
  return unwrap(api.POST('/me/discover/sets/{setId}/artists', { params: { path: { setId: id } }, body: { name } }))
}

export function removeArtist(id: string, key: string) {
  return unwrap(api.DELETE('/me/discover/sets/{setId}/artists/{artistKey}', { params: { path: { setId: id, artistKey: key } } }))
}

/** A thumb on a song. A thumbs-up wants it, and may grow the set (`grew`). */
export function thumb(kind: 'up' | 'down', item: DiscoveryItem, setId?: string) {
  return unwrap(
    api.POST('/me/discover/feedback', {
      body: {
        kind,
        setId,
        title: item.title,
        artist: item.artist,
        album: item.album,
        cover: item.cover,
        mbid: item.mbid,
        isrc: item.isrc,
        deezerId: item.deezerId,
      },
    }),
  )
}

export function block(name: string) {
  return unwrap(api.POST('/me/discover/blocks', { body: { name } }))
}

export function unblock(key: string) {
  return unwrap(api.DELETE('/me/discover/blocks/{artistKey}', { params: { path: { artistKey: key } } }))
}

export function refreshLibrary() {
  return unwrap(api.POST('/me/discover/library'))
}

// --- The Want list (MAD-824) -------------------------------------------------

export const wantQuery = queryOptions({
  queryKey: ['discover', 'want'],
  queryFn: () => unwrap(api.GET('/me/discover/want')),
})

/** Wants a release from a Fresh feed. */
export function wantRelease(r: DiscoveryRelease, setId?: string) {
  return unwrap(
    api.POST('/me/discover/want', {
      body: { kind: 'release', artist: r.artist, title: r.title, mbid: r.mbid, deezerId: r.deezerId, cover: r.cover, setId },
    }),
  )
}

export function setWant(id: string, status: WantStatus) {
  return unwrap(api.PATCH('/me/discover/want/{wantId}', { params: { path: { wantId: id } }, body: { status } }))
}

/** Notes that you saw what landed in your library. */
export function seeLanded() {
  return unwrap(api.POST('/me/discover/want/seen'))
}

/** A changed Want-list item, in the cached list. */
export function wantChanged(qc: QueryClient, item: WantItem) {
  qc.setQueryData<WantList>(wantQuery.queryKey, (l) => {
    if (!l) return l
    const i = l.items.findIndex((w) => w.id === item.id)
    return { ...l, items: i < 0 ? [item, ...l.items] : l.items.map((w) => (w.id === item.id ? item : w)) }
  })
  void qc.invalidateQueries({ queryKey: setsQuery.queryKey, exact: true })
}

/** A Want list's items with a status, grouped by artist: artists A to Z, each newest first. */
export function groupWants(items: readonly WantItem[], status: WantStatus) {
  const by = new Map<string, { artist: string; items: WantItem[] }>()
  for (const it of items) {
    if (it.status !== status) continue
    const k = it.artist.toLowerCase()
    const g = by.get(k) ?? { artist: it.artist, items: [] }
    g.items.push(it)
    by.set(k, g)
  }
  return [...by.values()].sort((a, b) => a.artist.localeCompare(b.artist, undefined, { sensitivity: 'base' }))
}

/** "3 wanted things landed in your library". */
export function landedText(n: number) {
  return `${n} wanted thing${n === 1 ? '' : 's'} landed in your library`
}

/** What a set has yielded: "2 of 5 wanted got", or "" if nothing yet. */
export function yieldText(s: Pick<DiscoverySetSummary, 'wanted' | 'got'>) {
  if (s.wanted === 0) return ''
  return `${s.got} of ${s.wanted} wanted got`
}

// --- Sets that learn (MAD-825) and rooms as a source (MAD-826) --------------

export const suggestionsQuery = (setId: string) =>
  queryOptions({
    queryKey: ['discover', 'suggestions', setId],
    queryFn: () => unwrap(api.GET('/me/discover/sets/{setId}/suggestions', { params: { path: { setId } } })),
  })

export function dismissSuggestion(setId: string, kind: 'grow' | 'block', artist: string) {
  return unwrap(api.POST('/me/discover/sets/{setId}/suggestions/dismiss', { params: { path: { setId } }, body: { kind, artist } }))
}

export type ArtistSource = 'manual' | 'thumb' | 'starred' | 'room'

/** Adds the artists you picked to a set, noted as coming from where you picked them. */
export function importArtists(setId: string, from: ArtistSource, names: string[]) {
  return unwrap(
    api.POST('/me/discover/sets/{setId}/import', { params: { path: { setId } }, body: { from, artists: names.map((name) => ({ name })) } }),
  )
}

export const starredQuery = queryOptions({
  queryKey: ['discover', 'starred'],
  queryFn: () => unwrap(api.GET('/me/discover/starred')),
  staleTime: 5 * 60_000,
})

export const roomTasteQuery = (q: { roomId: string; nightId?: string; mine?: boolean }) =>
  queryOptions({
    queryKey: ['discover', 'room-taste', q],
    queryFn: () =>
      unwrap(
        api.GET('/me/discover/room-taste', {
          params: { query: { roomId: q.roomId, ...(q.nightId ? { nightId: q.nightId } : {}), ...(q.mine ? { mine: true } : {}) } },
        }),
      ),
    enabled: !!q.roomId,
    staleTime: 60_000,
  })

/** "Starred · 2 albums · 1 song". */
export function starredText(a: DiscoveryStarredArtist) {
  const parts: string[] = []
  if (a.artist) parts.push('Starred')
  if (a.albums) parts.push(`${a.albums} album${a.albums === 1 ? '' : 's'}`)
  if (a.songs) parts.push(`${a.songs} song${a.songs === 1 ? '' : 's'}`)
  return parts.join(' · ')
}

const fromText: Record<ArtistSource, string> = {
  manual: '',
  thumb: 'Joined from your thumbs-up',
  starred: 'From your Navidrome stars',
  room: "From a room's taste",
}

/** Where a seed artist came from, for a hint; "" for ones you added. */
export function sourceText(from: ArtistSource) {
  return fromText[from] ?? ''
}

/** A feed's songs, once each: a feed rebuilt between pages can repeat one. */
export function feedItems(pages: readonly DiscoveryFeed[] | undefined): DiscoveryItem[] {
  const seen = new Set<string>()
  const out: DiscoveryItem[] = []
  for (const p of pages ?? []) {
    for (const it of p.items) {
      if (seen.has(it.key)) continue
      seen.add(it.key)
      out.push(it)
    }
  }
  return out
}

/** "Near Slowdive and Hum", or "More from Slowdive" for a seed's own song. */
export function becauseText(item: Pick<DiscoveryItem, 'because' | 'seed'>) {
  const names = item.because.filter(Boolean)
  if (names.length === 0) return ''
  if (item.seed) return `More from ${names[0]}`
  return `Near ${names.length === 1 ? names[0] : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`}`
}

/** A YouTube search for the song: the whole thing, in a new tab. */
export function youtubeUrl(item: Pick<DiscoveryItem, 'artist' | 'title'>) {
  return `https://www.youtube.com/results?search_query=${encodeURIComponent(`${item.artist} ${item.title}`)}`
}

/** A Deezer cover or artist picture, through the server. */
export function imageUrl(kind: 'cover' | 'artist', id: string | undefined, size = 250) {
  if (!id) return undefined
  return `/api/me/discover/images/${kind}/${encodeURIComponent(id)}?size=${size}`
}

/** A release's cover: Deezer's, else the Cover Art Archive's, through the server. */
export function releaseCoverUrl(r: Pick<DiscoveryRelease, 'cover' | 'coverRelease' | 'mbid'>, size = 250) {
  if (r.cover) return imageUrl('cover', r.cover, size)
  if (r.coverRelease) return `/api/me/discover/release-art/${encodeURIComponent(r.coverRelease)}?size=${size}`
  if (r.mbid) return `/api/me/discover/release-art/${encodeURIComponent(r.mbid)}?group=true&size=${size}`
  return undefined
}

/** A YouTube search for a release: the whole album, or the single. */
export function releaseYoutubeUrl(r: Pick<DiscoveryRelease, 'artist' | 'title' | 'kind'>) {
  const q = `${r.artist} ${r.title}${r.kind === 'single' ? '' : ' full album'}`
  return `https://www.youtube.com/results?search_query=${encodeURIComponent(q)}`
}

const kinds: Record<DiscoveryRelease['kind'], string> = {
  album: 'Album',
  ep: 'EP',
  single: 'Single',
  compilation: 'Compilation',
  live: 'Live album',
}

/** "EP". */
export function kindText(kind: DiscoveryRelease['kind']) {
  return kinds[kind] ?? 'Release'
}

const day = 24 * 3600_000

/** "Out today", "Out 3 days ago", "Out Sep 19", or "Out in September" when only the month is known. */
export function releaseDateText(date: string, now = Date.now()) {
  const [y, m, d] = date.split('-').map(Number)
  if (!y || !m) return ''
  if (!d) return `Out in ${new Date(Date.UTC(y, m - 1, 1)).toLocaleDateString(undefined, { month: 'long', timeZone: 'UTC' })}`
  const at = Date.UTC(y, m - 1, d)
  const today = new Date(now)
  const days = Math.round((Date.UTC(today.getFullYear(), today.getMonth(), today.getDate()) - at) / day)
  if (days <= 0) return 'Out today'
  if (days === 1) return 'Out yesterday'
  if (days < 7) return `Out ${days} days ago`
  return `Out ${new Date(at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })}`
}

/** A song or a release, in a feed that mixes them. */
export type FeedEntry = { kind: 'song'; item: DiscoveryItem } | { kind: 'release'; release: DiscoveryRelease }

/**
 * Mixes Fresh releases into a feed of songs ("Both"): the first after the
 * first song, then one every `every` songs. Releases left over go at the
 * end once the feed has no more pages (`done`), so they don't jump as
 * pages load.
 */
export function mixFresh(songs: readonly DiscoveryItem[], releases: readonly DiscoveryRelease[], done: boolean, every = 4): FeedEntry[] {
  const out: FeedEntry[] = []
  let r = 0
  songs.forEach((item, i) => {
    out.push({ kind: 'song', item })
    if (r < releases.length && (i === 0 || i % every === 0)) out.push({ kind: 'release', release: releases[r++] })
  })
  if (done) for (; r < releases.length; r++) out.push({ kind: 'release', release: releases[r] })
  return out
}

/** "1.2M fans". */
export function fansText(fans: number | undefined) {
  if (!fans) return ''
  const n = fans >= 1e6 ? `${(fans / 1e6).toFixed(fans >= 1e7 ? 0 : 1)}M` : fans >= 1e3 ? `${Math.round(fans / 1e3)}K` : String(fans)
  return `${n.replace('.0', '')} fan${fans === 1 ? '' : 's'}`
}

// --- Previews --------------------------------------------------------------

// One preview plays at a time, in its own audio element: it plays here
// only, and leaves the room's playback and Media Session alone.

type PreviewState = { key?: string; playing: boolean; progress: number; loading: boolean; failed?: string }

const preview = createStore<PreviewState>({ playing: false, progress: 0, loading: false })
let audio: HTMLAudioElement | undefined

function element() {
  if (audio) return audio
  const a = new Audio()
  a.preload = 'auto'
  const update = () =>
    preview.set((p) => ({ ...p, progress: a.duration > 0 ? a.currentTime / a.duration : 0, playing: !a.paused && !a.ended }))
  a.addEventListener('timeupdate', update)
  a.addEventListener('playing', () => preview.set((p) => ({ ...p, loading: false, playing: true })))
  a.addEventListener('pause', update)
  a.addEventListener('ended', () => preview.set((p) => ({ ...p, playing: false, progress: 1 })))
  a.addEventListener('error', () => preview.set((p) => ({ ...p, playing: false, loading: false, failed: p.key })))
  audio = a
  return a
}

/** Plays a song's preview, stopping any other; plays or pauses it if it's the one loaded. */
export function togglePreview(item: Pick<DiscoveryItem, 'key' | 'deezerId'>) {
  if (!item.deezerId) return
  const a = element()
  const cur = preview.get()
  if (cur.key === item.key && cur.failed !== item.key) {
    if (a.paused) {
      if (a.ended || cur.progress >= 1) a.currentTime = 0
      void a.play().catch(() => {})
    } else {
      a.pause()
    }
    return
  }
  a.pause()
  a.src = `/api/me/discover/preview/${item.deezerId}`
  preview.set({ key: item.key, playing: false, progress: 0, loading: true })
  void a.play().catch(() => preview.set((p) => ({ ...p, loading: false })))
}

/** Stops whatever preview is playing: leaving the page, say. */
export function stopPreview() {
  audio?.pause()
  preview.set((p) => ({ ...p, playing: false, loading: false }))
}

/** How one song's preview is doing. */
export function usePreview(key: string) {
  const p = useStore(preview)
  const mine = p.key === key
  return {
    playing: mine && p.playing,
    loading: mine && p.loading,
    progress: mine ? p.progress : 0,
    failed: p.failed === key,
  }
}
