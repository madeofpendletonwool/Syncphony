import { queryOptions, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import type { LaneTrack } from '@/hooks/use-add-to-lane'
import { sessionName } from './history'

// Syncphony's own playlists (MAD-737, ADR 0016): kept on the server, so
// one playlist can hold songs from everyone's services, like a night does.

export type SavedPlaylist = components['schemas']['SavedPlaylist']
export type SavedPlaylistSummary = components['schemas']['SavedPlaylistSummary']
export type PlaylistSong = components['schemas']['PlaylistSong']
export type SongToSave = components['schemas']['PlaylistSongToAdd']
export type Recap = components['schemas']['Recap']

export const savedPlaylistsQuery = queryOptions({
  queryKey: ['saved-playlists'],
  queryFn: () => unwrap(api.GET('/playlists')),
})

export const savedPlaylistQuery = (id: string) =>
  queryOptions({
    queryKey: ['saved-playlists', id],
    queryFn: () => unwrap(api.GET('/playlists/{playlistId}', { params: { path: { playlistId: id } } })),
  })

export const recapQuery = (roomId: string, from: string, to: string) =>
  queryOptions({
    queryKey: ['recap', roomId, from, to],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/recap', { params: { path: { roomId }, query: { from, to } } })),
  })

/** Puts a changed playlist in the cache, and refreshes the list. */
export function playlistChanged(qc: QueryClient, p: SavedPlaylist) {
  qc.setQueryData(savedPlaylistQuery(p.id).queryKey, p)
  void qc.invalidateQueries({ queryKey: savedPlaylistsQuery.queryKey, exact: true })
}

export function createPlaylist(name: string, roomId?: string) {
  return unwrap(api.POST('/playlists', { body: { name, roomId } }))
}

export function updatePlaylist(id: string, body: { name?: string; roomId?: string }) {
  return unwrap(api.PATCH('/playlists/{playlistId}', { params: { path: { playlistId: id } }, body }))
}

export function deletePlaylist(id: string) {
  return unwrap(api.DELETE('/playlists/{playlistId}', { params: { path: { playlistId: id } } }))
}

export function addSongs(id: string, items: SongToSave[]) {
  return unwrap(api.POST('/playlists/{playlistId}/songs', { params: { path: { playlistId: id } }, body: { items } }))
}

export function removeSong(id: string, songId: string) {
  return unwrap(api.DELETE('/playlists/{playlistId}/songs/{songId}', { params: { path: { playlistId: id, songId } } }))
}

export function moveSong(id: string, songId: string, position: number) {
  return unwrap(
    api.PUT('/playlists/{playlistId}/songs/{songId}/position', { params: { path: { playlistId: id, songId } }, body: { position } }),
  )
}

export type NightOptions = { name: string; from: string; to: string; keepSkipped: boolean; keepRepeats: boolean; share: boolean }

export function saveNight(roomId: string, o: NightOptions) {
  return unwrap(api.POST('/rooms/{roomId}/playlists', { params: { path: { roomId } }, body: o }))
}

/** "Living room · Friday night, Oct 9": what a saved night is called, until renamed. */
export function nightPlaylistName(roomName: string, startedAt: string) {
  const day = new Date(startedAt).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
  return `${roomName} · ${sessionName({ startedAt })}, ${day}`
}

export function songArtworkUrl(playlistId: string, song: PlaylistSong, size = 300) {
  if (!song.track.artwork || !song.track.linkId) return undefined
  return `/api/playlists/${encodeURIComponent(playlistId)}/songs/${encodeURIComponent(song.id)}/artwork?size=${size}`
}

/** A playlist song, to add to your lane. linkId is the song's own. */
export function laneTrackOfSong(song: PlaylistSong, linkId: string): LaneTrack {
  const t = song.track
  return {
    fromPlaylistSongId: song.id,
    linkId,
    provider: t.provider,
    trackId: t.trackId,
    title: t.title,
    artists: t.artists.map((name, i) => ({ name, id: t.artistIds?.[i] || undefined })),
    album: t.album ? { title: t.album, id: t.albumId } : undefined,
    durationMs: t.durationMs,
    explicit: t.explicit,
    artwork: t.artwork,
  }
}

/**
 * Whether you can queue a playlist song in a room: it came from a link
 * you can use, or the room lets people borrow.
 */
export function canQueue(song: PlaylistSong, usableLinkIds: ReadonlySet<string>, borrow: boolean) {
  const linkId = song.track.linkId
  return !!linkId && (borrow || usableLinkIds.has(linkId))
}

/** "12 songs · 48 min". */
export function playlistLength(songs: PlaylistSong[]) {
  const min = Math.round(songs.reduce((n, s) => n + s.track.durationMs, 0) / 60_000)
  const len = min < 60 ? `${min} min` : `${Math.floor(min / 60)} hr ${min % 60} min`
  return `${songs.length} song${songs.length === 1 ? '' : 's'}${songs.length > 0 ? ` · ${len}` : ''}`
}
