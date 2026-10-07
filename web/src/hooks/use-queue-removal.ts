import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { tap } from '@/lib/haptics'
import type { QueueItem } from '@/lib/playback'
import { queueQuery, type QueueSnapshot } from '@/lib/room'
import { toast } from '@/lib/toast'

/** How long Undo stays on screen. The server allows a minute. */
export const UNDO_MS = 5000

/** What a removal toast says: whose song it was, if it wasn't yours. */
export function removedMessage(title: string, owner?: string) {
  return owner ? `Removed “${title}” from ${owner}’s lane` : `Removed “${title}”`
}

/**
 * Takes songs out of a room's queue, one at a time or your whole lane,
 * with a few seconds to Undo. Undo puts them back where they were.
 */
export function useQueueRemoval(roomId: string) {
  const queryClient = useQueryClient()
  const show = (snap: QueueSnapshot) => queryClient.setQueryData(queueQuery(roomId).queryKey, snap)

  const restore = async (itemIds: string[]) => {
    try {
      show(await unwrap(api.POST('/rooms/{roomId}/queue/restore', { params: { path: { roomId } }, body: { itemIds } })))
      tap()
    } catch (err) {
      toast({ message: errorMessage(err), tone: 'error' })
    }
  }

  const offerUndo = (message: string, itemIds: string[]) =>
    toast({ message, action: { label: 'Undo', onClick: () => void restore(itemIds) } }, UNDO_MS)

  const remove = useMutation({
    // owner: whose lane it was in, when it's not yours.
    mutationFn: ({ item }: { item: QueueItem; owner?: string }) =>
      unwrap(api.DELETE('/rooms/{roomId}/queue/{itemId}', { params: { path: { roomId, itemId: item.id } } })),
    onMutate: () => tap(),
    onSuccess: (snap, { item, owner }) => {
      show(snap)
      offerUndo(removedMessage(item.track.title, owner), [item.id])
    },
    onError: (err) => toast({ message: errorMessage(err), tone: 'error' }),
  })

  const clear = useMutation({
    mutationFn: () => unwrap(api.DELETE('/rooms/{roomId}/lane', { params: { path: { roomId } } })),
    onMutate: () => tap(),
    onSuccess: ({ queue, removed }) => {
      show(queue)
      if (removed.length > 0) offerUndo(removed.length === 1 ? 'Cleared your lane' : `Cleared ${removed.length} songs from your lane`, removed)
    },
    onError: (err) => toast({ message: errorMessage(err), tone: 'error' }),
  })

  return { remove, clear }
}
