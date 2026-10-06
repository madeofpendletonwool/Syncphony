import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { isMine } from './autopilot'
import { toLocalTime } from './clock'
import type { NowPlaying } from './now-playing'
import type { Room } from './room'

export type Playback = components['schemas']['NowPlaying']
export type QueueItem = components['schemas']['QueueItem']
export type PlaybackCommand = components['schemas']['PlaybackCommand']

/** What the room is playing. The room socket keeps it fresh. */
export const playbackQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['playback', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/playback', { params: { path: { roomId } } })),
  })

/** Songs the room played recently, newest first. Refetch when the song changes. */
export const historyQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['history', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/history', { params: { path: { roomId }, query: { limit: 20 } } })),
  })

export function sendCommand(roomId: string, body: PlaybackCommand) {
  return unwrap(api.POST('/rooms/{roomId}/playback', { params: { path: { roomId } }, body }))
}

/** The newer of two playback states, by revision (then server time). */
export function newer(old: Playback | undefined, np: Playback) {
  if (!old || old.roomId !== np.roomId) return np
  if (old.revision !== np.revision) return old.revision > np.revision ? old : np
  return Date.parse(old.at) > Date.parse(np.at) ? old : np
}

/** A queued song's artwork, loaded through whoever queued it. */
export function queueArtworkUrl(roomId: string, item: QueueItem, size = 300) {
  if (!item.track.artwork || !item.track.linkId) return undefined
  return `/api/rooms/${encodeURIComponent(roomId)}/queue/${encodeURIComponent(item.id)}/artwork?size=${size}`
}

export type Permission = keyof Room['permissions']

/**
 * Whether userId may do something in room outright. The owner always may.
 * In a room that votes on skips, everyone else votes instead (skipMode).
 */
export function can(room: Pick<Room, 'permissions' | 'ownerId'>, userId: string, permission: Permission) {
  return room.ownerId === userId || room.permissions[permission] === 'everyone'
}

/**
 * How userId can skip item: outright, by voting, or not at all. Whoever
 * queued a song can always skip it; autopilot's songs are nobody's.
 */
export function skipMode(room: Pick<Room, 'permissions' | 'ownerId'>, userId: string, item: Pick<QueueItem, 'addedBy' | 'autopilot'>) {
  if (isMine(item, userId) || can(room, userId, 'skip')) return 'skip'
  return room.permissions.skip === 'vote' ? 'vote' : undefined
}

/**
 * How many songs play before your next one: 0 means you're up next.
 * Undefined if you have nothing waiting.
 */
export function songsBeforeYours(upNext: string[], items: QueueItem[], userId: string) {
  const mine = new Set(items.filter((i) => isMine(i, userId)).map((i) => i.id))
  const i = upNext.findIndex((id) => mine.has(id))
  return i < 0 ? undefined : i
}

/** The shell player's view of the room's playback. */
export function toNowPlaying(roomId: string, p: Playback, users: NowPlaying['requester'][] | undefined): NowPlaying | null {
  if (!p.item) return null
  return {
    roomId,
    itemId: p.item.id,
    track: p.item.track,
    via: p.item.via?.provider,
    artworkUrl: queueArtworkUrl(roomId, p.item, 600),
    palette: p.item.palette,
    requester: p.item.autopilot ? undefined : users?.find((u) => u?.id === p.item?.addedBy),
    autopilot: p.item.autopilot,
    paused: p.state !== 'playing',
    positionMs: p.positionMs,
    at: toLocalTime(Date.parse(p.at)),
  }
}
