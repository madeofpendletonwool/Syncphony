import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useRoomControls } from '@/hooks/use-room-controls'
import { useMe } from '@/lib/auth'
import { player } from '@/lib/now-playing'
import { newer, playbackQuery, toNowPlaying } from '@/lib/playback'
import { useCurrentRoom } from '@/lib/room'
import { useRoomSocket } from '@/lib/room-socket'
import { speaker, speakerState } from '@/lib/speaker'
import { usersQuery } from '@/lib/users'

/**
 * Keeps the current room live while you're signed in, on every screen,
 * and feeds the shell's mini-player and now-playing view.
 */
export function RoomLive() {
  const me = useMe()
  const { room, rooms } = useCurrentRoom()
  useRoomSocket(room?.id)
  const playback = useQuery({ ...playbackQuery(room?.id ?? ''), enabled: !!room })
  const users = useQuery(usersQuery)
  const commands = useRoomControls(room, playback.data, me, !!me.guest)
  const queryClient = useQueryClient()

  // Player mode: whatever the server says, the speaker (if this device is
  // one) applies; its reports' replies flow back into the cache.
  useEffect(() => {
    speaker.onState = (np) => queryClient.setQueryData(playbackQuery(np.roomId).queryKey, (old) => newer(old, np))
  }, [queryClient])
  useEffect(() => speaker.apply(playback.data), [playback.data])
  // Switching rooms, or leaving, stops playing the old one here.
  useEffect(() => {
    if (speaker.active && rooms.isSuccess && speakerRoom() !== room?.id) void speaker.stop()
  }, [room, rooms.isSuccess])
  // Signing out stops the speaker. Going to the big screen hands it over
  // instead, and coming back keeps whatever it's playing.
  useEffect(() => () => speaker.stopSoon(), [])
  useEffect(() => {
    if (room) speaker.keep(room.id)
  }, [room])

  // On the next frame, so a burst of updates (a phone waking with songs'
  // worth of events queued up) shows only the last, and none while hidden.
  // The speaker reads the cache directly, so it keeps up in the background.
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const np = playback.data && room ? toNowPlaying(room.id, playback.data, users.data) : null
      player.set({ nowPlaying: np, commands })
    })
    return () => cancelAnimationFrame(frame)
  }, [room, playback.data, users.data, commands])

  // Leaving the signed-in screens (signing out) empties the player.
  useEffect(() => () => player.set({ nowPlaying: null, commands: {} }), [])

  return null
}

const speakerRoom = () => speakerState.get().roomId
