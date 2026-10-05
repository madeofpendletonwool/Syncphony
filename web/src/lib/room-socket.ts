import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import type { components } from '@/api/schema.gen'
import { meQuery } from './auth'
import { newer, playbackQuery, type Playback } from './playback'
import { queueQuery, type QueueSnapshot } from './room'
import { linksQuery } from './services'
import { createStore } from './store'
import { toast } from './toast'
import { usersQuery } from './users'

type User = components['schemas']['User']
type ServiceLink = components['schemas']['ServiceLink']
type RoomEvent = { type: string; version?: number; data: unknown }

export type Live = {
  roomId?: string
  status: 'connecting' | 'live' | 'reconnecting'
  /** Who has the room open right now. */
  members: User[]
}

export const live = createStore<Live>({ status: 'connecting', members: [] })

const SESSION_ENDED = 4001
const FELL_BEHIND = 1013

/**
 * Keeps the current room's queue and playback live over its WebSocket:
 * every event lands in the query cache, so screens just read queries.
 * Reconnects with backoff and resumes from the last queue version.
 */
export function useRoomSocket(roomId: string | undefined) {
  const queryClient = useQueryClient()

  useEffect(() => {
    if (!roomId) return
    let ws: WebSocket | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    let attempt = 0
    let closed = false
    live.set({ roomId, status: 'connecting', members: [] })

    const connect = () => {
      clearTimeout(retry)
      const version = queryClient.getQueryData(queueQuery(roomId).queryKey)?.version
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      const url = `${proto}://${location.host}/ws/rooms/${encodeURIComponent(roomId)}${version !== undefined ? `?since=${version}` : ''}`
      const sock = new WebSocket(url)
      ws = sock
      sock.onmessage = (e) => {
        attempt = 0
        handle(queryClient, roomId, JSON.parse(e.data as string) as RoomEvent)
      }
      sock.onclose = (e) => {
        if (closed || ws !== sock) return
        if (e.code === SESSION_ENDED) {
          queryClient.setQueryData(meQuery.queryKey, null)
          return
        }
        live.set((l) => ({ ...l, status: 'reconnecting' }))
        // Falling behind is our cue to resume right away; anything else backs off.
        const delay = e.code === FELL_BEHIND ? 0 : Math.min(15_000, 500 * 2 ** attempt++)
        retry = setTimeout(connect, delay)
      }
    }

    // Phones drop sockets in the background; come back as soon as we can.
    const wake = () => {
      if (document.visibilityState === 'visible' && ws?.readyState !== WebSocket.OPEN && ws?.readyState !== WebSocket.CONNECTING) {
        attempt = 0
        connect()
      }
    }
    document.addEventListener('visibilitychange', wake)
    window.addEventListener('online', wake)
    connect()

    return () => {
      closed = true
      clearTimeout(retry)
      document.removeEventListener('visibilitychange', wake)
      window.removeEventListener('online', wake)
      ws?.close()
    }
  }, [roomId, queryClient])
}

function handle(queryClient: QueryClient, roomId: string, ev: RoomEvent) {
  switch (ev.type) {
    case 'hello': {
      const hello = ev.data as components['schemas']['RoomHello']
      live.set({ roomId, status: 'live', members: hello.members })
      hello.members.forEach((u) => upsertUser(queryClient, u))
      break
    }
    case 'queue.updated': {
      const snap = ev.data as QueueSnapshot
      queryClient.setQueryData(queueQuery(roomId).queryKey, (old) => (old && old.version > snap.version ? old : snap))
      break
    }
    case 'nowplaying.updated': {
      const np = ev.data as Playback
      queryClient.setQueryData(playbackQuery(roomId).queryKey, (old) => newer(old, np))
      break
    }
    case 'playback.notice':
      toast({ message: (ev.data as components['schemas']['PlaybackNotice']).message })
      break
    case 'member.joined': {
      const u = ev.data as User
      upsertUser(queryClient, u)
      live.set((l) => ({ ...l, members: [...l.members.filter((m) => m.id !== u.id), u] }))
      break
    }
    case 'member.left': {
      const u = ev.data as User
      live.set((l) => ({ ...l, members: l.members.filter((m) => m.id !== u.id) }))
      break
    }
    case 'link.status': {
      const link = ev.data as ServiceLink
      queryClient.setQueryData(linksQuery.queryKey, (ls) => ls?.map((l) => (l.id === link.id ? link : l)))
      break
    }
  }
}

/** Someone new may have signed up since we loaded the user list. */
function upsertUser(queryClient: QueryClient, u: User) {
  queryClient.setQueryData(usersQuery.queryKey, (us) => {
    if (!us) return us
    return us.some((x) => x.id === u.id) ? us.map((x) => (x.id === u.id ? u : x)) : [...us, u]
  })
}
