import { useQueryClient } from '@tanstack/react-query'
import { useMemo } from 'react'
import { errorMessage } from '@/api/errors'
import { tap } from '@/lib/haptics'
import type { PlayerCommands } from '@/lib/now-playing'
import { isMine } from '@/lib/autopilot'
import { can, playbackQuery, sendCommand, skipMode, type Permission, type Playback, type PlaybackCommand } from '@/lib/playback'
import type { Room } from '@/lib/room'
import { toast } from '@/lib/toast'

/**
 * Transport controls for the room, limited to what its permissions let you
 * do. The owner can do anything and anyone can skip their own song; in a
 * room that votes on skips, everyone else gets a vote instead of `next`.
 * Guests only skip their own songs, and vote if the room lets them.
 */
export function useRoomControls(room: Room | undefined, playback: Playback | undefined, userId: string, guest = false): PlayerCommands {
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
    const playing = playback.state === 'playing' || playback.state === 'loading'
    const allowed = (p: Permission) => !guest && can(room, userId, p)
    let skip = skipMode(room, userId, item)
    if (guest && !isMine(item, userId)) skip = skip === 'vote' && room.guests.canVote ? 'vote' : undefined
    const votes = playback.skipVotes
    const voted = !!votes?.voters.includes(userId)
    return {
      toggle: allowed('playPause')
        ? () =>
            playing
              ? send({ action: 'pause' }, (p) => ({ ...p, state: 'paused', positionMs: currentPosition(p), at: new Date().toISOString() }))
              : send({ action: 'play' })
        : undefined,
      // There's no going back in a fair queue; "previous" restarts the song,
      // like most players do past the first few seconds.
      previous: allowed('seek') ? () => send({ action: 'seek', positionMs: 0 }, (p) => ({ ...p, positionMs: 0, at: new Date().toISOString() })) : undefined,
      next: skip === 'skip' ? () => send({ action: 'skip', itemId: item.id }) : undefined,
      vote:
        skip === 'vote' && votes
          ? {
              voted,
              count: votes.voters.length,
              needed: votes.needed,
              toggle: () =>
                send({ action: voted ? 'unvote_skip' : 'vote_skip', itemId: item.id }, (p) => ({
                  ...p,
                  skipVotes: p.skipVotes && {
                    ...p.skipVotes,
                    voters: voted ? p.skipVotes.voters.filter((id) => id !== userId) : [...p.skipVotes.voters, userId],
                  },
                })),
            }
          : undefined,
      seek: allowed('seek')
        ? (positionMs: number) => send({ action: 'seek', positionMs }, (p) => ({ ...p, positionMs, at: new Date().toISOString() }))
        : undefined,
    }
  }, [room, playback, userId, guest, queryClient])
}

/** Where a playing song is now, from its position at the last report. */
export function currentPosition(p: Playback, now = Date.now()) {
  if (p.state !== 'playing') return p.positionMs
  return p.positionMs + Math.max(0, now - Date.parse(p.at))
}
