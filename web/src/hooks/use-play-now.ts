import { useQuery, useQueryClient } from '@tanstack/react-query'
import { errorMessage } from '@/api/errors'
import { useMe } from '@/lib/auth'
import { tap } from '@/lib/haptics'
import { can, playbackQuery, sendCommand, type PlaybackCommand, type QueueItem } from '@/lib/playback'
import { roomsQuery } from '@/lib/room'
import { speaker, speakerName } from '@/lib/speaker'
import { toast } from '@/lib/toast'

export type PlayNow = {
  /** `now` plays it straight away; `ask` asks the room, which votes. */
  mode: 'now' | 'ask'
  play: (item: QueueItem) => void
}

/**
 * Playing a queued song now, skipping the one on. It's the skip
 * permission's call: in a room that votes on skips, you ask the room
 * instead (the owner never has to). Guests can't. With no speaker, this
 * device becomes it, in the same tap, if it may.
 */
export function usePlayNow(roomId: string): PlayNow | undefined {
  const me = useMe()
  const rooms = useQuery(roomsQuery)
  const command = usePlaybackCommand(roomId)
  const queryClient = useQueryClient()
  const room = rooms.data?.find((r) => r.id === roomId)
  if (!room || me.guest) return undefined
  const skip = room.permissions.skip
  const mode = room.ownerId === me.id || skip === 'everyone' ? 'now' : skip === 'vote' ? 'ask' : undefined
  if (!mode) return undefined
  return {
    mode,
    play: (item) => {
      tap()
      const ask = () =>
        command({ action: 'play_now', itemId: item.id }).then((np) => {
          if (np?.playNow?.itemId === item.id) toast({ message: `Asked the room to play “${item.track.title}”` })
        })
      const playback = queryClient.getQueryData(playbackQuery(roomId).queryKey)
      if (playback?.player || !can(room, me.id, 'speaker')) return void ask()
      // Becoming the speaker plays the song straight away if we may;
      // otherwise the room starts and we ask it, as anyone would.
      void speaker.start(roomId, speakerName(me.displayName), undefined, item.id).then((np) => {
        if (np && np.item?.id !== item.id) void ask()
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
