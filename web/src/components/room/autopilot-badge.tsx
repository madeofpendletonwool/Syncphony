import { useMutation, useQueryClient } from '@tanstack/react-query'
import { WandSparkles, X } from 'lucide-react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { autopilotReason, autopilotSource, type AutopilotPick } from '@/lib/autopilot'
import { tap } from '@/lib/haptics'
import type { QueueItem } from '@/lib/playback'
import { queueQuery } from '@/lib/room'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'

/** Marks a song autopilot chose, in place of who added it. */
export function AutopilotBadge({ pick, className }: { pick: AutopilotPick; className?: string }) {
  return (
    <Badge className={cn('gap-1.5 py-1', className)} title={autopilotReason(pick)}>
      <WandSparkles aria-hidden />
      Autopilot
    </Badge>
  )
}

/** Why autopilot chose a song, and where the DJ's knowledge came from. */
export function AutopilotWhy({ pick, className }: { pick: AutopilotPick; className?: string }) {
  const source = autopilotSource(pick)
  return (
    <p className={cn('text-pretty text-muted-foreground', className)}>
      <span className="text-primary">{autopilotReason(pick)}</span>
      {source && <span className="text-muted-foreground/70"> · {source}</span>}
    </p>
  )
}

/** Autopilot's stand-in for a user avatar, in queue rows. */
export function AutopilotMark({ className }: { className?: string }) {
  return (
    <span
      role="img"
      aria-label="Autopilot"
      className={cn('grid size-6 shrink-0 place-items-center rounded-full bg-primary/15 text-primary', className)}
    >
      <WandSparkles className="size-3.5" aria-hidden />
    </span>
  )
}

/**
 * Takes a waiting autopilot song out of the queue. Anyone may: it's
 * nobody's. Autopilot then picks something else, and not that song.
 */
export function NotThisOne({ roomId, item }: { roomId: string; item: QueueItem }) {
  const queryClient = useQueryClient()
  const remove = useMutation({
    mutationFn: () => unwrap(api.DELETE('/rooms/{roomId}/queue/{itemId}', { params: { path: { roomId, itemId: item.id } } })),
    onMutate: () => tap(),
    onSuccess: (snap) => {
      queryClient.setQueryData(queueQuery(roomId).queryKey, snap)
      toast({ message: `Autopilot will pick something other than “${item.track.title}”` })
    },
    onError: (err) => toast({ message: errorMessage(err), tone: 'error' }),
  })
  return (
    <Button
      size="icon-sm"
      variant="ghost"
      aria-label={`Not “${item.track.title}”`}
      title="Not this one"
      disabled={remove.isPending}
      onClick={() => remove.mutate()}
    >
      <X />
    </Button>
  )
}
