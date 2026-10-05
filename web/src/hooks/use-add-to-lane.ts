import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { useMe } from '@/lib/auth'
import { trackKey, type TrackResult } from '@/lib/browse'
import { tap } from '@/lib/haptics'
import { queueQuery, useCurrentRoom, type QueueSnapshot } from '@/lib/room'
import { createStore, useStore } from '@/lib/store'
import { toast } from '@/lib/toast'

export type LaneStatus = 'idle' | 'adding' | 'added'

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
  const adding = useStore(pending)

  // Songs already waiting (or playing) in your lane.
  const inLane = useMemo(
    () =>
      new Set(
        queue.data?.items
          .filter((i) => i.addedBy === me.id && (i.state === 'queued' || i.state === 'playing'))
          .map((i) => trackKey(i.track)),
      ),
    [queue.data, me.id],
  )

  const mutation = useMutation({
    mutationFn: (tracks: TrackResult[]) => {
      if (!room) throw new Error('no room')
      return unwrap(
        api.POST('/rooms/{roomId}/queue', {
          params: { path: { roomId: room.id } },
          body: { items: tracks.map((t) => ({ linkId: t.linkId, trackId: t.trackId })) },
        }),
      )
    },
    onMutate: (tracks) => {
      tap()
      setPending(tracks.map(trackKey), true)
    },
    onSuccess: (snap, tracks) => {
      queryClient.setQueryData(queueQuery(snap.roomId).queryKey, snap)
      const added = lastAdded(snap, me.id, tracks.length)
      toast({
        message: tracks.length === 1 ? `Added “${tracks[0].title}” to your lane` : `Added ${tracks.length} songs to your lane`,
        action: added.length > 0 ? { label: 'Undo', onClick: () => void undo(snap.roomId, added) } : undefined,
      })
    },
    onError: (err) => toast({ message: room ? errorMessage(err) : 'Join a room first.', tone: 'error' }),
    onSettled: (_data, _err, tracks) => setPending(tracks.map(trackKey), false),
  })

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

  return { add: mutation.mutate, status, room }
}

/**
 * The `n` songs just added to the end of your lane: songs are always added
 * at the end, all together.
 */
export function lastAdded(snap: QueueSnapshot, userId: string, n: number) {
  if (n <= 0) return []
  return snap.items
    .filter((i) => i.addedBy === userId && i.state === 'queued')
    .sort((a, b) => a.lanePosition - b.lanePosition)
    .slice(-n)
    .map((i) => i.id)
}
