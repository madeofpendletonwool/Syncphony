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
}

export function createSet(name: string, artists: { name: string; mbid?: string }[] = []) {
  return unwrap(api.POST('/me/discover/sets', { body: { name, artists } }))
}

export function updateSet(id: string, body: { name?: string; explore?: number; mode?: DiscoveryMode }) {
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

export function thumb(kind: 'up' | 'down', item: DiscoveryItem, setId?: string) {
  return unwrap(
    api.POST('/me/discover/feedback', {
      body: { kind, setId, title: item.title, artist: item.artist, mbid: item.mbid, isrc: item.isrc, deezerId: item.deezerId },
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
