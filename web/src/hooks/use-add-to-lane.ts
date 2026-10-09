import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { useMe } from '@/lib/auth'
import { isMine } from '@/lib/autopilot'
import { trackKey, type TrackResult } from '@/lib/browse'
import { duplicateMessage, DuplicatesFound } from '@/lib/duplicates'
import { tap } from '@/lib/haptics'
import type { QueueItem } from '@/lib/playback'
import { queueQuery, useCurrentRoom, type QueueSnapshot } from '@/lib/room'
import { createStore, useStore } from '@/lib/store'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'

export type LaneStatus = 'idle' | 'adding' | 'added'

/**
 * A song to add: from search, one the room had (`fromItemId`), again, or
 * one from a Syncphony playlist (`fromPlaylistSongId`).
 */
export type LaneTrack = TrackResult & { fromItemId?: string; fromPlaylistSongId?: string }

/** A song the room had, to add again. linkId is the item's own (it has one). */
export function laneTrackOf(item: QueueItem, linkId: string): LaneTrack {
  const t = item.track
  return {
    fromItemId: item.id,
    linkId,
    provider: t.provider,
    trackId: t.trackId,
    title: t.title,
    artists: t.artists.map((name, i) => ({ name, id: t.artistIds?.[i] || undefined })),
    album: t.album ? { title: t.album, id: t.albumId } : undefined,
    durationMs: t.durationMs,
    explicit: t.explicit,
    artwork: t.artwork,
  }
}

// Tracks being added right now, shared by every list on screen, so a song
// shows as added the moment it's tapped.
const pending = createStore<ReadonlySet<string>>(new Set())

function setPending(keys: string[], on: boolean) {
  pending.set((prev) => {
    const next = new Set(prev)
    for (const k of keys) {
      if (on) next.add(k)
      else next.delete(k)
    }
    return next
  })
}

/** Adds songs to the end of your lane in the current room. */
export function useAddToLane() {
  const me = useMe()
  const { room } = useCurrentRoom()
  const queryClient = useQueryClient()
  const queue = useQuery({ ...queueQuery(room?.id ?? ''), enabled: !!room })
  const users = useQuery(usersQuery)
  const adding = useStore(pending)

  // Songs already waiting (or playing) in your lane.
  const inLane = useMemo(
    () =>
      new Set(
        queue.data?.items
          .filter((i) => isMine(i, me.id) && (i.state === 'queued' || i.state === 'playing'))
          .map((i) => trackKey(i.track)),
      ),
    [queue.data, me.id],
  )

  const mutation = useMutation({
    mutationFn: async ({ tracks, anyway }: { tracks: LaneTrack[]; anyway?: boolean }) => {
      if (!room) throw new Error('no room')
      const call = api.POST('/rooms/{roomId}/queue', {
        params: { path: { roomId: room.id } },
        body: {
          items: tracks.map((t) =>
            t.fromItemId
              ? { fromItemId: t.fromItemId }
              : t.fromPlaylistSongId
                ? { fromPlaylistSongId: t.fromPlaylistSongId }
                : { linkId: t.linkId, trackId: t.trackId },
          ),
          warnDuplicates: !anyway,
        },
      })
      const { error } = await call
      if (error && 'duplicates' in error && error.code === 'duplicate' && error.duplicates) throw new DuplicatesFound(error.duplicates)
      return unwrap(call)
    },
    onMutate: ({ tracks }) => {
      tap()
      setPending(tracks.map(trackKey), true)
    },
    onSuccess: (snap, { tracks }) => {
      queryClient.setQueryData(queueQuery(snap.roomId).queryKey, snap)
      const added = lastAdded(snap, me.id, tracks.length)
      toast({
        message: tracks.length === 1 ? `Added “${tracks[0].title}” to your lane` : `Added ${tracks.length} songs to your lane`,
        action: added.length > 0 ? { label: 'Undo', onClick: () => void undo(snap.roomId, added) } : undefined,
      })
    },
    onError: (err, { tracks }) => {
      if (err instanceof DuplicatesFound) {
        const message = duplicateMessage(err.duplicates, me.id, (id) => users.data?.find((u) => u.id === id)?.displayName)
        toast({ message, action: { label: 'Add anyway', onClick: () => mutation.mutate({ tracks, anyway: true }) } }, 6000)
        return
      }
      toast({ message: room ? errorMessage(err) : 'Join a room first.', tone: 'error' })
    },
    onSettled: (_data, _err, { tracks }) => setPending(tracks.map(trackKey), false),
  })
  const { mutate } = mutation
  const add = useCallback((tracks: LaneTrack[]) => mutate({ tracks }), [mutate])

  const undo = async (roomId: string, ids: string[]) => {
    try {
      let snap: QueueSnapshot | undefined
      for (const itemId of ids) {
        snap = await unwrap(api.DELETE('/rooms/{roomId}/queue/{itemId}', { params: { path: { roomId, itemId } } }))
      }
      if (snap) queryClient.setQueryData(queueQuery(roomId).queryKey, snap)
      toast({ message: ids.length === 1 ? 'Removed from your lane' : `Removed ${ids.length} songs` })
    } catch (err) {
      toast({ message: errorMessage(err), tone: 'error' })
    }
  }

  const status = useCallback(
    (t: TrackResult): LaneStatus => {
      const k = trackKey(t)
      return adding.has(k) ? 'adding' : inLane.has(k) ? 'added' : 'idle'
    },
    [adding, inLane],
  )

  return { add, status, room, queue: queue.data }
}

/**
 * The `n` songs just added to the end of your lane: songs are always added
 * at the end, all together.
 */
export function lastAdded(snap: QueueSnapshot, userId: string, n: number) {
  if (n <= 0) return []
  return snap.items
    .filter((i) => isMine(i, userId) && i.state === 'queued')
    .sort((a, b) => a.lanePosition - b.lanePosition)
    .slice(-n)
    .map((i) => i.id)
}
