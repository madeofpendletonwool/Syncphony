import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

export type TrackResult = components['schemas']['TrackResult']
export type AlbumResult = components['schemas']['AlbumResult']
export type ArtistResult = components['schemas']['ArtistResult']
export type SearchGroup = components['schemas']['SearchGroup']

export const searchQuery = (q: string) =>
  queryOptions({
    queryKey: ['search', q],
    queryFn: ({ signal }) => unwrap(api.GET('/search', { params: { query: { q } }, signal })),
    staleTime: 5 * 60_000,
  })

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
