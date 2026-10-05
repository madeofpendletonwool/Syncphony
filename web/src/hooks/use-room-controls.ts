import { useQueryClient } from '@tanstack/react-query'
import { useMemo } from 'react'
import { errorMessage } from '@/api/errors'
import { tap } from '@/lib/haptics'
import type { PlayerCommands } from '@/lib/now-playing'
import { canControl, playbackQuery, sendCommand, type Playback, type PlaybackCommand } from '@/lib/playback'
import type { Room } from '@/lib/room'
import { toast } from '@/lib/toast'

/**
 * Transport controls for the room, limited to what you're allowed to do:
 * the owner (or everyone, if the room says so) can play, pause, seek and
 * skip; anyone can skip their own song.
 */
export function useRoomControls(room: Room | undefined, playback: Playback | undefined, userId: string): PlayerCommands {
  const queryClient = useQueryClient()

  return useMemo(() => {
    if (!room || !playback?.item) return {}
    const key = playbackQuery(room.id).queryKey
    const item = playback.item
    const send = async (cmd: PlaybackCommand, optimistic?: (p: Playback) => Playback) => {
      tap()
      const before = queryClient.getQueryData(key)
      if (before && optimistic) queryClient.setQueryData(key, optimistic(before))
      try {
        queryClient.setQueryData(key, await sendCommand(room.id, cmd))
      } catch (err) {
        if (before) queryClient.setQueryData(key, before)
        toast({ message: errorMessage(err), tone: 'error' })
      }
    }
    const full = canControl(room, userId)
    const playing = playback.state === 'playing' || playback.state === 'loading'
    return {
      toggle: full
        ? () =>
            playing
              ? send({ action: 'pause' }, (p) => ({ ...p, state: 'paused', positionMs: currentPosition(p), at: new Date().toISOString() }))
              : send({ action: 'play' })
        : undefined,
      next: full || item.addedBy === userId ? () => send({ action: 'skip', itemId: item.id }) : undefined,
      seek: full
        ? (positionMs: number) => send({ action: 'seek', positionMs }, (p) => ({ ...p, positionMs, at: new Date().toISOString() }))
        : undefined,
    }
  }, [room, playback, userId, queryClient])
}

/** Where a playing song is now, from its position at the last report. */
export function currentPosition(p: Playback, now = Date.now()) {
  if (p.state !== 'playing') return p.positionMs
  return p.positionMs + Math.max(0, now - Date.parse(p.at))
}
