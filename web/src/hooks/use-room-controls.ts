import { useQueryClient } from '@tanstack/react-query'
import { useMemo } from 'react'
import { errorMessage } from '@/api/errors'
import { tap } from '@/lib/haptics'
import type { PlayerCommands } from '@/lib/now-playing'
import { isMine } from '@/lib/autopilot'
import {
  BACK_WITHIN_MS,
  can,
  playbackQuery,
  sendCommand,
  skipMode,
  type Permission,
  type Playback,
  type PlaybackCommand,
} from '@/lib/playback'
import type { Room } from '@/lib/room'
import { speaker, speakerName } from '@/lib/speaker'
import { toast } from '@/lib/toast'

/**
 * Transport controls for the room, limited to what its permissions let you
 * do. The owner can do anything and anyone can skip their own song; in a
 * room that votes on skips, everyone else gets a vote instead of `next`.
 * Guests only skip their own songs, and vote if the room lets them.
 */
export function useRoomControls(
  room: Room | undefined,
  playback: Playback | undefined,
  me: { id: string; displayName: string },
  guest = false,
): PlayerCommands {
  const queryClient = useQueryClient()
  const userId = me.id

  return useMemo(() => {
    if (!room || !playback) return {}
    const key = playbackQuery(room.id).queryKey
    const send = async (cmd: PlaybackCommand, optimistic?: (p: Playback) => Playback) => {
      tap()
      const before = queryClient.getQueryData(key)
      if (before && optimistic) queryClient.setQueryData(key, optimistic(before))
      try {
        const np = await sendCommand(room.id, cmd)
        queryClient.setQueryData(key, np)
        return np
      } catch (err) {
        if (before) queryClient.setQueryData(key, before)
        toast({ message: errorMessage(err), tone: 'error' })
      }
    }
    const playing = playback.state === 'playing' || playback.state === 'loading'
    const allowed = (p: Permission) => !guest && can(room, userId, p)
    // With no speaker, play makes this device it and starts the room, in
    // the one tap (browsers only start audio from a tap).
    const play = async () => {
      if (!playback.player && allowed('speaker')) {
        const np = await speaker.start(room.id, speakerName(me.displayName))
        if (!np || np.state === 'playing' || np.state === 'loading') return
      }
      await send({ action: 'play' })
    }
    const toggle = allowed('playPause')
      ? () =>
          playing
            ? send({ action: 'pause' }, (p) => ({ ...p, state: 'paused', positionMs: currentPosition(p), at: new Date().toISOString() }))
            : void play()
      : undefined
    const item = playback.item
    // Nothing's on: play is all there is (it starts the next song).
    if (!item) return { toggle }
    let skip = skipMode(room, userId, item)
    if (guest && !isMine(item, userId)) skip = skip === 'vote' && room.guests.canVote ? 'vote' : undefined
    const votes = playback.skipVotes
    const voted = !!votes?.voters.includes(userId)
    const restart = allowed('seek')
      ? () => send({ action: 'seek', positionMs: 0 }, (p) => ({ ...p, positionMs: 0, at: new Date().toISOString() }))
      : undefined
    // Going back ends the song playing, so it's the skip permission's
    // call: in a room that votes on skips, it asks the room.
    const goBack =
      !guest && (can(room, userId, 'skip') || room.permissions.skip === 'vote')
        ? () =>
            send({ action: 'previous' }).then((np) => {
              if (np?.playNow?.back && np.playNow.by === userId) {
                toast({ message: `Asked the room to go back to “${np.playNow.item?.track.title ?? 'the last song'}”` })
              }
            })
        : undefined
    return {
      toggle,
      // Restarts the song, and goes back to the one before in its first
      // few seconds, like most players.
      previous:
        restart || goBack
          ? () => void (restart && (!goBack || currentPosition(playback) > BACK_WITHIN_MS) ? restart() : goBack?.())
          : undefined,
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
  }, [room, playback, userId, me.displayName, guest, queryClient])
}

/** Where a playing song is now, from its position at the last report. */
export function currentPosition(p: Playback, now = Date.now()) {
  if (p.state !== 'playing') return p.positionMs
  return p.positionMs + Math.max(0, now - Date.parse(p.at))
}
