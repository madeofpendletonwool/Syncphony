import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useRoomControls } from '@/hooks/use-room-controls'
import { useMe } from '@/lib/auth'
import { player } from '@/lib/now-playing'
import { playbackQuery, toNowPlaying } from '@/lib/playback'
import { useCurrentRoom } from '@/lib/room'
import { useRoomSocket } from '@/lib/room-socket'
import { usersQuery } from '@/lib/users'

/**
 * Keeps the current room live while you're signed in, on every screen,
 * and feeds the shell's mini-player and now-playing view.
 */
export function RoomLive() {
  const me = useMe()
  const { room } = useCurrentRoom()
  useRoomSocket(room?.id)
  const playback = useQuery({ ...playbackQuery(room?.id ?? ''), enabled: !!room })
  const users = useQuery(usersQuery)
  const commands = useRoomControls(room, playback.data, me.id)

  useEffect(() => {
    const np = playback.data && room ? toNowPlaying(room.id, playback.data, users.data) : null
    player.set({ nowPlaying: np, commands })
  }, [room, playback.data, users.data, commands])

  // Leaving the signed-in screens (signing out) empties the player.
  useEffect(() => () => player.set({ nowPlaying: null, commands: {} }), [])

  return null
}
