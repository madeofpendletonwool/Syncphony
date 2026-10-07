import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useQueueRemoval } from '@/hooks/use-queue-removal'
import type { User } from '@/lib/now-playing'
import type { QueueItem } from '@/lib/playback'

/** For the room's owner: takes someone's song out of the queue, with a moment to Undo. */
export function RemoveTheirs({ roomId, item, owner }: { roomId: string; item: QueueItem; owner?: User }) {
  const { remove } = useQueueRemoval(roomId)
  return (
    <Button
      size="icon-sm"
      variant="ghost"
      aria-label={`Remove “${item.track.title}”`}
      title="Remove from the queue"
      disabled={remove.isPending}
      onClick={() => remove.mutate({ item, owner: owner?.displayName ?? 'someone' })}
      className="hover:bg-destructive/15 hover:text-destructive"
    >
      <X />
    </Button>
  )
}
