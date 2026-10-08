import { useQuery, useQueryClient } from '@tanstack/react-query'
import { errorMessage } from '@/api/errors'
import { useMe } from '@/lib/auth'
import { tap } from '@/lib/haptics'
import { playbackQuery, sendCommand, type PlaybackCommand, type QueueItem } from '@/lib/playback'
import { roomsQuery } from '@/lib/room'
import { toast } from '@/lib/toast'

export type PlayNow = {
  /** `now` plays it straight away; `ask` asks the room, which votes. */
  mode: 'now' | 'ask'
  play: (item: QueueItem) => void
}

/**
 * Playing a queued song now, skipping the one on. It's the skip
 * permission's call: in a room that votes on skips, you ask the room
 * instead (the owner never has to). Guests can't.
 */
export function usePlayNow(roomId: string): PlayNow | undefined {
  const me = useMe()
  const rooms = useQuery(roomsQuery)
  const command = usePlaybackCommand(roomId)
  const room = rooms.data?.find((r) => r.id === roomId)
  if (!room || me.guest) return undefined
  const skip = room.permissions.skip
  const mode = room.ownerId === me.id || skip === 'everyone' ? 'now' : skip === 'vote' ? 'ask' : undefined
  if (!mode) return undefined
  return {
    mode,
    play: (item) => {
      tap()
      void command({ action: 'play_now', itemId: item.id }).then((np) => {
        if (np?.playNow?.itemId === item.id) toast({ message: `Asked the room to play “${item.track.title}”` })
      })
    },
  }
}

/** Sends a playback command and keeps the cache in step, or says why it failed. */
export function usePlaybackCommand(roomId: string) {
  const queryClient = useQueryClient()
  return async (body: PlaybackCommand) => {
    try {
      const np = await sendCommand(roomId, body)
      queryClient.setQueryData(playbackQuery(roomId).queryKey, np)
      return np
    } catch (err) {
      toast({ message: errorMessage(err), tone: 'error' })
    }
  }
}
