import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { createStore } from './store'

// Song of the night (MAD-721): hearts on the songs as they play, and the
// night's most-hearted song crowned when the night ends.

export type Hearts = components['schemas']['Hearts']
export type Night = components['schemas']['Night']

export const heartsQuery = (roomId: string, itemId: string) =>
  queryOptions({
    queryKey: ['hearts', roomId, itemId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/queue/{itemId}/hearts', { params: { path: { roomId, itemId } } })),
  })

export function setHeart(roomId: string, itemId: string, on: boolean) {
  const params = { params: { path: { roomId, itemId } } }
  return unwrap(on ? api.PUT('/rooms/{roomId}/queue/{itemId}/hearts', params) : api.DELETE('/rooms/{roomId}/queue/{itemId}/hearts', params))
}

export const nightsQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['nights', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/nights', { params: { path: { roomId }, query: { limit: 30 } } })),
  })

export function endNight(roomId: string) {
  return unwrap(api.POST('/rooms/{roomId}/nights', { params: { path: { roomId } } }))
}

/** The night that just ended, while its crowning is on screen. */
export const crowning = createStore<Night | null>(null)

/** How long the crowning stays up. */
export const CROWN_MS = 20_000

let clear: ReturnType<typeof setTimeout> | undefined

/** Puts a night that just ended on screen, for a while: longer with awards to read. */
export function crown(n: Night) {
  crowning.set(n)
  clearTimeout(clear)
  clear = setTimeout(() => crowning.set(null), n.awards.length > 0 ? CROWN_MS * 2 : CROWN_MS)
}

export function dismissCrown() {
  clearTimeout(clear)
  crowning.set(null)
}
