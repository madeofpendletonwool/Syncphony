import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

export type TrackResult = components['schemas']['TrackResult']
export type AlbumResult = components['schemas']['AlbumResult']
export type ArtistResult = components['schemas']['ArtistResult']
export type SearchGroup = components['schemas']['SearchGroup']
export type PlaylistResult = components['schemas']['PlaylistResult']
export type LinkCollection = components['schemas']['LinkCollection']
type Provider = components['schemas']['ProviderInfo']

declare module '@tanstack/react-router' {
  interface HistoryState {
    /** The playlist being opened, so a public one that isn't in the link's list still has a name. */
    playlist?: PlaylistResult
  }
}

/** Searches every link; with `publicPlaylists`, services that can also look through everyone's playlists. */
export const searchQuery = (q: string, publicPlaylists = false) =>
  queryOptions({
    queryKey: ['search', q, publicPlaylists],
    queryFn: ({ signal }) =>
      unwrap(api.GET('/search', { params: { query: publicPlaylists ? { q, publicPlaylists } : { q } }, signal })),
    staleTime: 5 * 60_000,
  })

/** Whether any of the searched services can look through public playlists. */
export function searchesPublicPlaylists(groups: SearchGroup[], providers: Provider[] | undefined) {
  return groups.some((g) => !g.error && providers?.find((p) => p.id === g.provider)?.capabilities.search.includes('playlist'))
}

export const albumQuery = (linkId: string, albumId: string) =>
  queryOptions({
    queryKey: ['album', linkId, albumId],
    queryFn: () => unwrap(api.GET('/links/{id}/albums/{albumId}', { params: { path: { id: linkId, albumId } } })),
    staleTime: 5 * 60_000,
  })

export const artistQuery = (linkId: string, artistId: string) =>
  queryOptions({
    queryKey: ['artist', linkId, artistId],
    queryFn: () => unwrap(api.GET('/links/{id}/artists/{artistId}', { params: { path: { id: linkId, artistId } } })),
    staleTime: 5 * 60_000,
  })

/** A link's playlists. Only the first page is fetched: the services with playlists list them all at once. */
export const playlistsQuery = (linkId: string) =>
  queryOptions({
    queryKey: ['playlists', linkId],
    queryFn: () => unwrap(api.GET('/links/{id}/playlists', { params: { path: { id: linkId } } })),
    staleTime: 5 * 60_000,
  })

/** A link's saved albums and artists, and its album lists, for browsing before searching. */
export const collectionQuery = (linkId: string) =>
  queryOptions({
    queryKey: ['collection', linkId],
    queryFn: () => unwrap(api.GET('/links/{id}/collection', { params: { path: { id: linkId } } })),
    staleTime: 5 * 60_000,
  })

/** Songs like one the room queued, from the services you can use. */
export const similarQuery = (roomId: string, itemId: string) =>
  queryOptions({
    queryKey: ['similar', roomId, itemId],
    queryFn: () =>
      unwrap(api.GET('/rooms/{roomId}/queue/{itemId}/similar', { params: { path: { roomId, itemId }, query: { limit: 10 } } })),
    staleTime: 10 * 60_000,
  })

/** Songs picked at random from the services you can use. */
export function randomTracks(limit: number) {
  return unwrap(api.GET('/random-tracks', { params: { query: { limit } } }))
}

/** A playlist's tracks, page by page. */
export const playlistTracksQuery = (linkId: string, playlistId: string) =>
  infiniteQueryOptions({
    queryKey: ['playlist', linkId, playlistId],
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/links/{id}/playlists/{playlistId}/tracks', {
          params: { path: { id: linkId, playlistId }, query: pageParam ? { cursor: pageParam } : {} },
        }),
      ),
    initialPageParam: '',
    getNextPageParam: (page) => page.next,
    staleTime: 5 * 60_000,
  })

/** A browser URL for an `artwork` ref from search or browse results. */
export function artworkUrl(linkId: string, ref: string | undefined, size = 300) {
  if (!ref) return undefined
  const q = new URLSearchParams({ ref, size: String(size) })
  return `/api/links/${encodeURIComponent(linkId)}/artwork?${q}`
}

/** Identifies a track across services: the same song through two links is two tracks. */
export const trackKey = (t: { linkId?: string; trackId: string }) => `${t.linkId ?? ''}:${t.trackId}`

/**
 * Mixes each link's results into one list, taking turns, so one service
 * doesn't bury the others. Each item keeps the link it came from.
 */
export function interleave<T>(groups: SearchGroup[], pick: (g: SearchGroup) => T[]): (T & { linkId: string })[] {
  const lists = groups.map((g) => pick(g).map((item) => ({ ...item, linkId: g.linkId })))
  const out: (T & { linkId: string })[] = []
  for (let i = 0; lists.some((l) => i < l.length); i++) {
    for (const l of lists) if (i < l.length) out.push(l[i])
  }
  return out
}

/** "Artist A, Artist B" */
export const artistNames = (artists: { name: string }[]) => artists.map((a) => a.name).join(', ')

/** Total running time, like "42 min" or "1 hr 5 min". */
export function totalDuration(tracks: { durationMs: number }[]) {
  const min = Math.round(tracks.reduce((n, t) => n + t.durationMs, 0) / 60_000)
  if (min < 60) return `${min} min`
  return `${Math.floor(min / 60)} hr ${min % 60} min`
}

/** n items picked at random, in random order. */
export function pickRandom<T>(items: T[], n: number, random = Math.random): T[] {
  const out = [...items]
  for (let i = 0; i < Math.min(n, out.length); i++) {
    const j = i + Math.floor(random() * (out.length - i))
    ;[out[i], out[j]] = [out[j], out[i]]
  }
  return out.slice(0, n)
}
