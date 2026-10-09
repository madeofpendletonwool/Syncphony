import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import type { components } from '@/api/schema.gen'
import { invitesQuery, membersQuery } from './access'
import { meQuery } from './auth'
import { syncServerClock } from './clock'
import { displayMeQuery } from './displays'
import { playRoundClips } from './game-clips'
import { gameRoundQuery, gameScoresQuery, type GameRound, type GameScores } from './games'
import { guestPassQuery, guestsQuery } from './guests'
import { crown, heartsQuery, nightsQuery, type Hearts, type Night } from './nights'
import { newer, playbackQuery, type Playback } from './playback'
import { putQueueGame, queueGamesQuery, type QueueGame } from './queue-games'
import { addReaction, type Reaction } from './reactions'
import { leaveRoom, queueQuery, roomsQuery, type QueueSnapshot, type Room } from './room'
import { linksQuery, usableLinksQuery } from './services'
import { deviceId } from './speaker'
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
const NO_ACCESS = 4003
const ROOM_GONE = 4004
const FELL_BEHIND = 1013

type SocketOptions = {
  /** A big screen: watches the room without counting as being in it. */
  display?: boolean
  /** The session (or the display's pairing) ended. Default: signs out. */
  onSessionEnded?: () => void
  /** The device ID this screen plays the room as, if not this browser's (a paired display's). */
  device?: string
}

/**
 * Keeps the current room's queue and playback live over its WebSocket:
 * every event lands in the query cache, so screens just read queries.
 * Reconnects with backoff and resumes from the last queue version.
 */
export function useRoomSocket(roomId: string | undefined, { display = false, onSessionEnded, device }: SocketOptions = {}) {
  const ended = useRef(onSessionEnded)
  useEffect(() => {
    ended.current = onSessionEnded
  })
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
      const query = new URLSearchParams()
      if (version !== undefined) query.set('since', String(version))
      if (display) query.set('display', '1')
      // The speaker sees what a game round hides: its lock screen shows the song anyway.
      query.set('device', device ?? deviceId())
      const qs = query.toString()
      const url = `${proto}://${location.host}/ws/rooms/${encodeURIComponent(roomId)}${qs ? `?${qs}` : ''}`
      const sock = new WebSocket(url)
      ws = sock
      sock.onmessage = (e) => {
        attempt = 0
        handle(queryClient, roomId, JSON.parse(e.data as string) as RoomEvent)
      }
      sock.onclose = (e) => {
        if (closed || ws !== sock) return
        if (e.code === SESSION_ENDED) {
          if (ended.current) ended.current()
          else queryClient.setQueryData(meQuery.queryKey, null)
          return
        }
        if (e.code === ROOM_GONE) return
        if (e.code === NO_ACCESS) {
          // Removed, or the room closed to us: it's not ours to see any more.
          const name = queryClient.getQueryData(roomsQuery.queryKey)?.find((r) => r.id === roomId)?.name
          queryClient.setQueryData(roomsQuery.queryKey, (rs) => rs?.filter((r) => r.id !== roomId))
          leaveRoom()
          toast({ message: name ? `You're no longer in ${name}` : "You're no longer in that room" })
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
  }, [roomId, display, device, queryClient])
}

function handle(queryClient: QueryClient, roomId: string, ev: RoomEvent) {
  switch (ev.type) {
    case 'hello': {
      const hello = ev.data as components['schemas']['RoomHello']
      syncServerClock(hello.serverTime)
      live.set({ roomId, status: 'live', members: hello.members })
      // The games still up follow, one event each.
      queryClient.setQueryData(queueGamesQuery(roomId).queryKey, [])
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
    case 'room.updated': {
      const room = ev.data as Room
      queryClient.setQueryData(roomsQuery.queryKey, (rs) => rs?.map((r) => (r.id === room.id ? room : r)))
      // A paired screen knows its room from its own record: its look follows the room's.
      queryClient.setQueryData(displayMeQuery.queryKey, (d) => (d && d.room.id === room.id ? { ...d, room } : d))
      break
    }
    case 'room.deleted': {
      const { roomId: gone } = ev.data as { roomId: string }
      queryClient.setQueryData(roomsQuery.queryKey, (rs) => rs?.filter((r) => r.id !== gone))
      toast({ message: 'This room was deleted.' })
      break
    }
    case 'reaction.sent':
      addReaction(ev.data as Reaction)
      break
    case 'hearts.updated': {
      const h = ev.data as Hearts
      queryClient.setQueryData(heartsQuery(roomId, h.itemId).queryKey, h)
      break
    }
    case 'night.ended': {
      crown(ev.data as Night)
      void queryClient.invalidateQueries({ queryKey: nightsQuery(roomId).queryKey })
      void queryClient.invalidateQueries({ queryKey: ['sessions', roomId] })
      break
    }
    case 'game.round': {
      const round = ev.data as GameRound
      queryClient.setQueryData(gameRoundQuery(roomId).queryKey, round.state === 'done' ? null : round)
      playRoundClips(round)
      // Lyrics and liner notes asked for while the round hid them come back now.
      if (round.state === 'reveal') {
        for (const key of [['lyrics', roomId, round.itemId], ['liner-notes', roomId, round.itemId]]) {
          if (queryClient.getQueryState(key)?.status === 'error') void queryClient.invalidateQueries({ queryKey: key })
        }
        // Lyrics fetched during a round have its line blanked.
        if (round.hides.includes('line')) void queryClient.invalidateQueries({ queryKey: ['lyrics', roomId, round.itemId] })
      }
      break
    }
    case 'game.scores':
      queryClient.setQueryData(gameScoresQuery(roomId).queryKey, ev.data as GameScores)
      break
    case 'game.queue':
      putQueueGame(queryClient, ev.data as QueueGame)
      break
    case 'guests.updated':
      void queryClient.invalidateQueries({ queryKey: guestPassQuery(roomId).queryKey })
      void queryClient.invalidateQueries({ queryKey: guestsQuery(roomId).queryKey })
      void queryClient.invalidateQueries({ queryKey: ['users'] })
      break
    case 'members.updated': {
      const m = ev.data as components['schemas']['RoomMembersChanged']
      void queryClient.invalidateQueries({ queryKey: membersQuery(roomId).queryKey })
      void queryClient.invalidateQueries({ queryKey: invitesQuery(roomId).queryKey })
      const me = queryClient.getQueryData(meQuery.queryKey)
      const room = queryClient.getQueryData(roomsQuery.queryKey)?.find((r) => r.id === roomId)
      if (m.change === 'requested' && me && room && (room.ownerId === me.id || me.role === 'admin')) {
        const who = queryClient.getQueryData(usersQuery.queryKey)?.find((u) => u.id === m.userId)
        toast({ message: `${who?.displayName ?? 'Someone'} asked to join ${room.name}. Let them in from Members.` }, 6000)
      }
      break
    }
    case 'link.status': {
      const link = ev.data as ServiceLink
      queryClient.setQueryData(linksQuery.queryKey, (ls) => ls?.map((l) => (l.id === link.id ? link : l)))
      queryClient.setQueryData(usableLinksQuery.queryKey, (ls) => ls?.map((l) => (l.id === link.id ? link : l)))
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
