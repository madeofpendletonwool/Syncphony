import { queryOptions, useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { createStore, useStore } from './store'

export type Room = components['schemas']['Room']
export type QueueSnapshot = components['schemas']['QueueSnapshot']

export const roomsQuery = queryOptions({
  queryKey: ['rooms'],
  queryFn: () => unwrap(api.GET('/rooms')),
  staleTime: 60_000,
})

export const queueQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['queue', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/queue', { params: { path: { roomId } } })),
  })

const KEY = 'syncphony-room'

function readChoice() {
  try {
    return localStorage.getItem(KEY) ?? undefined
  } catch {
    return undefined
  }
}

const chosen = createStore<string | undefined>(readChoice())

/** Remembers which room this device is in. */
export function chooseRoom(id: string) {
  try {
    localStorage.setItem(KEY, id)
  } catch {
    // Private mode: the choice lasts until reload.
  }
  chosen.set(id)
}

/**
 * The room this device is in: the one chosen here before, or else the
 * first room on the server. `room` is undefined while loading or when the
 * server has no rooms yet.
 */
export function useCurrentRoom() {
  const rooms = useQuery(roomsQuery)
  const id = useStore(chosen)
  const room = rooms.data?.find((r) => r.id === id) ?? rooms.data?.[0]
  return { room, rooms }
}
