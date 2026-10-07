import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import type { AlbumResult } from '@/lib/browse'

export type ArtistAbout = components['schemas']['ArtistAbout']
export type ArtistMember = components['schemas']['ArtistMember']
export type AlbumKind = components['schemas']['AlbumKind']
export type RelatedArtist = components['schemas']['RelatedArtist']
export type ArtistElsewhere = components['schemas']['ArtistElsewhere']
export type AlbumAbout = components['schemas']['AlbumAbout']

// What's known about artists changes slowly; the server caches it for days.
const SLOW = 30 * 60_000

/** An artist's bio, facts, members, genres and the kinds of their albums. */
export const artistAboutQuery = (linkId: string, artistId: string) =>
  queryOptions({
    queryKey: ['artist', linkId, artistId, 'about'],
    queryFn: () => unwrap(api.GET('/links/{id}/artists/{artistId}/about', { params: { path: { id: linkId, artistId } } })),
    staleTime: SLOW,
  })

/** An artist's top songs, and songs on others' records they're on. */
export const artistTracksQuery = (linkId: string, artistId: string) =>
  queryOptions({
    queryKey: ['artist', linkId, artistId, 'tracks'],
    queryFn: () => unwrap(api.GET('/links/{id}/artists/{artistId}/tracks', { params: { path: { id: linkId, artistId } } })),
    staleTime: SLOW,
  })

/** Artists like an artist, and songs by them. */
export const artistRelatedQuery = (linkId: string, artistId: string) =>
  queryOptions({
    queryKey: ['artist', linkId, artistId, 'related'],
    queryFn: () => unwrap(api.GET('/links/{id}/artists/{artistId}/related', { params: { path: { id: linkId, artistId } } })),
    staleTime: SLOW,
  })

/** The same artist on your other services. */
export const artistElsewhereQuery = (linkId: string, artistId: string) =>
  queryOptions({
    queryKey: ['artist', linkId, artistId, 'elsewhere'],
    queryFn: () => unwrap(api.GET('/links/{id}/artists/{artistId}/elsewhere', { params: { path: { id: linkId, artistId } } })),
    staleTime: SLOW,
  })

/** Songs by an artist at random, fresh every time. */
export function artistShuffle(linkId: string, artistId: string, limit = 10) {
  return unwrap(
    api.GET('/links/{id}/artists/{artistId}/shuffle', { params: { path: { id: linkId, artistId }, query: { limit } } }),
  )
}

/** The artist's songs this room played most. */
export const roomArtistPlaysQuery = (roomId: string, name: string) =>
  queryOptions({
    queryKey: ['history', roomId, 'artist', name],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/artist-plays', { params: { path: { roomId }, query: { name, limit: 5 } } })),
    staleTime: 60_000,
  })

/** A genre's artists, and songs by them, through a link. */
export const genreQuery = (linkId: string, name: string) =>
  queryOptions({
    queryKey: ['genre', linkId, name.toLowerCase()],
    queryFn: () => unwrap(api.GET('/links/{id}/genre', { params: { path: { id: linkId }, query: { name } } })),
    staleTime: SLOW,
  })

/** An album's kind, first release, labels, genres and summary. */
export const albumAboutQuery = (linkId: string, albumId: string) =>
  queryOptions({
    queryKey: ['album', linkId, albumId, 'about'],
    queryFn: () => unwrap(api.GET('/links/{id}/albums/{albumId}/about', { params: { path: { id: linkId, albumId } } })),
    staleTime: SLOW,
    retry: false,
  })

/** The shelves a discography splits into, in order. */
export const shelves = [
  { title: 'Albums', kinds: ['album'] },
  { title: 'Singles & EPs', kinds: ['single', 'ep'] },
  { title: 'Live', kinds: ['live'] },
  { title: 'Compilations', kinds: ['compilation'] },
] as const satisfies readonly { title: string; kinds: readonly AlbumKind[] }[]

/** An album with the link it's on, for discographies mixing services. */
export type LinkedAlbum = AlbumResult & { linkId: string }

/** What kind an album is: what its service says, else MusicBrainz, else an album. */
export function kindOf(album: AlbumResult, kinds: Record<string, AlbumKind> | undefined): AlbumKind {
  return album.kind ?? kinds?.[album.id] ?? 'album'
}

/** "Single", "EP", "Live album"... for an album's subtitle. */
export function kindLabel(kind: AlbumKind) {
  return { album: 'Album', single: 'Single', ep: 'EP', live: 'Live album', compilation: 'Compilation' }[kind]
}

/**
 * Splits a discography into shelves, newest first, leaving out empty ones.
 * `kinds` are the kinds MusicBrainz knows of the albums whose service
 * doesn't say.
 */
export function discography(albums: LinkedAlbum[], kinds?: Record<string, AlbumKind>) {
  const newest = [...albums].sort((a, b) => (b.year ?? 0) - (a.year ?? 0))
  return shelves
    .map((s) => ({ title: s.title, albums: newest.filter((a) => (s.kinds as readonly AlbumKind[]).includes(kindOf(a, kinds))) }))
    .filter((s) => s.albums.length > 0)
}

/** An album title without edition notes, to compare: "OK Computer (Deluxe)" is "ok computer". */
export function albumKey(title: string) {
  return title
    .toLowerCase()
    .replace(/\s*[([].*?[)\]]/g, '')
    .replace(/\s+-\s+.*$/, '')
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/&/g, ' and ')
    .replace(/[^\p{L}\p{N}]+/gu, ' ')
    .trim()
}

/**
 * Adds other services' albums to a discography: only those it doesn't
 * already have, by title, each once.
 */
export function mergeAlbums(own: LinkedAlbum[], others: LinkedAlbum[][]): LinkedAlbum[] {
  const seen = new Set(own.map((a) => albumKey(a.title)))
  const out = [...own]
  for (const list of others) {
    for (const a of list) {
      const k = albumKey(a.title)
      if (seen.has(k)) continue
      seen.add(k)
      out.push(a)
    }
  }
  return out
}

/** When someone was in a band: "1994–2012", "Since 1996", "Until 1999". */
export function memberYears(m: ArtistMember) {
  if (m.from && m.to) return m.from === m.to ? String(m.from) : `${m.from}–${m.to}`
  if (m.from) return m.current ? `Since ${m.from}` : `From ${m.from}`
  if (m.to) return `Until ${m.to}`
  return ''
}

/** "guitar, lead vocals" → "Guitar, lead vocals". */
export function rolesLine(roles: string[]) {
  const s = roles.join(', ')
  return s.charAt(0).toUpperCase() + s.slice(1)
}
