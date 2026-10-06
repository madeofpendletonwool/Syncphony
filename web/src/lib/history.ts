import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

export type PlayedItem = components['schemas']['PlayedItem']
export type RoomStats = components['schemas']['RoomStats']
export type ListeningSession = components['schemas']['ListeningSession']

const PAGE = 30

/** A room's history, a page at a time, optionally one person's songs. */
export const historyPagesQuery = (roomId: string, userId?: string) =>
  infiniteQueryOptions({
    queryKey: ['history', roomId, 'pages', userId ?? ''],
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/rooms/{roomId}/history', {
          params: { path: { roomId }, query: { limit: PAGE, before: pageParam, userId } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.length < PAGE ? undefined : last.at(-1)?.startedAt),
  })

/** One person's latest songs in a room, newest first. */
export const myHistoryQuery = (roomId: string, userId: string) =>
  queryOptions({
    queryKey: ['history', roomId, 'recent', userId],
    queryFn: () =>
      unwrap(api.GET('/rooms/{roomId}/history', { params: { path: { roomId }, query: { limit: 40, userId } } })),
    staleTime: 60_000,
  })

/** A stretch of time; open ends mean all of it. */
export type Range = { from?: string; to?: string }

export const statsQuery = (roomId: string, range: Range = {}) =>
  queryOptions({
    queryKey: ['stats', roomId, range.from ?? '', range.to ?? ''],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/stats', { params: { path: { roomId }, query: range } })),
  })

export const sessionsQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['sessions', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/sessions', { params: { path: { roomId }, query: { limit: 30 } } })),
  })

/** The time range a session's recap covers. `to` is exclusive, so it reaches just past the end. */
export function sessionRange(s: ListeningSession): Range {
  return { from: s.startedAt, to: new Date(Date.parse(s.endedAt) + 1).toISOString() }
}

/** "Friday night", "Sunday afternoon": when a session was, roughly. */
export function sessionName(s: Pick<ListeningSession, 'startedAt'>) {
  const d = new Date(s.startedAt)
  const day = d.toLocaleDateString(undefined, { weekday: 'long' })
  const h = d.getHours()
  const part = h < 5 ? 'late night' : h < 12 ? 'morning' : h < 17 ? 'afternoon' : h < 21 ? 'evening' : 'night'
  return `${day} ${part}`
}

/** "2 h 15 min", "45 min". */
export function formatListening(ms: number) {
  const minutes = Math.round(ms / 60_000)
  if (minutes < 60) return `${minutes} min`
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return m === 0 ? `${h} h` : `${h} h ${m} min`
}
