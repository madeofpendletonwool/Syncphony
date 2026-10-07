import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { queueQuery, type QueueSnapshot } from '@/lib/room'
import { toast } from '@/lib/toast'

/**
 * Moves one of your queued songs within your lane — position 0 is the
 * front — and puts the room's new queue everywhere it's on screen.
 */
export function useQueueMove(roomId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ itemId, position }: { itemId: string; position: number }) =>
      unwrap(api.PATCH('/rooms/{roomId}/queue/{itemId}', { params: { path: { roomId, itemId } }, body: { position } })),
    onSuccess: (snap: QueueSnapshot) => queryClient.setQueryData(queueQuery(roomId).queryKey, snap),
    onError: (err) => toast({ message: errorMessage(err), tone: 'error' }),
  })
}
