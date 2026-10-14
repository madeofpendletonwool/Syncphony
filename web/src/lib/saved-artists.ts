import { queryOptions, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import type { NowPlaying } from '@/lib/now-playing'

// Saved artists (MAD-839..841): artists you want to remember, in your
// Library beside playlists. Personal, and on no particular service.

export type SavedArtist = components['schemas']['SavedArtist']
/** An artist to save: by name (and where it's from), or one of a room's song's artists. */
export type ArtistToSave = { name: string; linkId?: string; artistId?: string; itemId?: string; artistIndex?: number }

export const savedArtistsQuery = queryOptions({
  queryKey: ['saved-artists'],
  queryFn: () => unwrap(api.GET('/me/saved-artists')),
  staleTime: 60_000,
})

export function saveArtist(a: ArtistToSave) {
  return unwrap(api.POST('/me/saved-artists', { body: a.itemId ? { itemId: a.itemId, artistIndex: a.artistIndex ?? 0 } : { name: a.name, linkId: a.linkId, artistId: a.artistId, artistIndex: 0 } }))
}

export function unsaveArtist(id: string) {
  return unwrap(api.DELETE('/me/saved-artists/{id}', { params: { path: { id } } }))
}

/** Puts a just-saved artist at the top of the cached list, then refetches it. */
export function artistSaved(qc: QueryClient, a: SavedArtist) {
  qc.setQueryData(savedArtistsQuery.queryKey, (old) => (old ? [a, ...old.filter((x) => x.id !== a.id)] : old))
  return qc.invalidateQueries({ queryKey: savedArtistsQuery.queryKey })
}

export function artistUnsaved(qc: QueryClient, id: string) {
  qc.setQueryData(savedArtistsQuery.queryKey, (old) => old?.filter((x) => x.id !== id))
  return qc.invalidateQueries({ queryKey: savedArtistsQuery.queryKey })
}

/**
 * The name an artist is known by, as the server's match.Simplify makes it:
 * lowercase, no accents or punctuation, "&" as "and". Two names with the
 * same key are the same saved artist.
 */
export function artistKey(name: string) {
  return name
    .toLowerCase()
    .normalize('NFD')
    .replace(/\p{Mn}/gu, '')
    .replace(/['’]/g, '')
    .replace(/&/g, ' and ')
    .replace(/[^\p{L}\p{N}]+/gu, ' ')
    .trim()
}

/** The saved artist with name, if it's saved. */
export function findSaved(saved: SavedArtist[] | undefined, name: string) {
  const key = artistKey(name)
  return saved?.find((a) => a.key === key)
}

/** The playing song's artists, to save from the room (which remembers where you heard them). */
export function nowPlayingArtists(np: Pick<NowPlaying, 'itemId' | 'track'>): ArtistToSave[] {
  return np.track.artists.map((name, i) =>
    np.itemId
      ? { name, itemId: np.itemId, artistIndex: i }
      : { name, linkId: np.track.linkId, artistId: np.track.artistIds?.[i] || undefined },
  )
}

/** Where to open a saved artist: the newest link it was seen on that you can still use. */
export function openableRef(a: Pick<SavedArtist, 'refs'>, usable: { id: string }[] | undefined) {
  return a.refs.find((r) => usable?.some((l) => l.id === r.linkId))
}

export type ArtistSort = 'recent' | 'name'

/** Saved artists matching filter (by name, loosely), in sort's order. */
export function arrangeSaved(saved: SavedArtist[], sort: ArtistSort, filter = '') {
  const want = artistKey(filter)
  const kept = want ? saved.filter((a) => a.key.includes(want)) : saved
  return sort === 'name' ? [...kept].sort((a, b) => a.key.localeCompare(b.key)) : kept
}
