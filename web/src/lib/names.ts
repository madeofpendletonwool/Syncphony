import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

// Linked names (MAD-837): every artist and album name opens its page. A name
// heard on someone else's service is looked up by name on yours.

export type ResolvedRef = components['schemas']['ResolvedRef']

/** An artist as credited somewhere: the name, and its ID on the link it came from when known. */
export type Credit = { name: string; id?: string }

/** A queued song's artists, each with its ID where the service gave one. */
export function trackCredits(track: { artists: string[]; artistIds?: string[] }): Credit[] {
  return track.artists.map((name, i) => ({ name, id: track.artistIds?.[i] || undefined }))
}

/** The link to open a page through directly: `linkId`, if you can use it and the page's ID is known. */
export function directLink(linkId: string | undefined, id: string | undefined, usable: { id: string }[] | undefined) {
  return linkId && id && usable?.some((l) => l.id === linkId) ? linkId : undefined
}

const key = (s: string) => s.trim().toLowerCase()

export const resolveArtistQuery = (name: string) =>
  queryOptions({
    queryKey: ['resolve', 'artist', key(name)],
    queryFn: () => unwrap(api.GET('/resolve/artist', { params: { query: { name } } })),
    staleTime: 30 * 60_000,
    retry: false,
  })

export const resolveAlbumQuery = (title: string, artist?: string) =>
  queryOptions({
    queryKey: ['resolve', 'album', key(title), key(artist ?? '')],
    queryFn: () => unwrap(api.GET('/resolve/album', { params: { query: artist ? { title, artist } : { title } } })),
    staleTime: 30 * 60_000,
    retry: false,
  })

export type NameTarget =
  | { to: '/artist/$linkId/$artistId'; params: { linkId: string; artistId: string } }
  | { to: '/album/$linkId/$albumId'; params: { linkId: string; albumId: string } }
  | { to: '/search'; search: { q: string; tab: 'artists' | 'albums' } }

/** Where an artist's name leads once looked up: its page, or a search for it when no service has it. */
export async function artistTarget(name: string, resolve: () => Promise<ResolvedRef>): Promise<NameTarget> {
  try {
    const r = await resolve()
    return { to: '/artist/$linkId/$artistId', params: { linkId: r.linkId, artistId: r.id } }
  } catch {
    return { to: '/search', search: { q: name, tab: 'artists' } }
  }
}

/** Where an album's title leads once looked up, like `artistTarget`. */
export async function albumTarget(title: string, resolve: () => Promise<ResolvedRef>): Promise<NameTarget> {
  try {
    const r = await resolve()
    return { to: '/album/$linkId/$albumId', params: { linkId: r.linkId, albumId: r.id } }
  } catch {
    return { to: '/search', search: { q: title, tab: 'albums' } }
  }
}
