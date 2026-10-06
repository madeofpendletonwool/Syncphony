import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Heart } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { errorMessage } from '@/api/errors'
import { useMe } from '@/lib/auth'
import { tap } from '@/lib/haptics'
import { heartsQuery, setHeart, type Hearts } from '@/lib/nights'
import { spring } from '@/lib/motion'
import type { NowPlaying } from '@/lib/now-playing'
import type { Room } from '@/lib/room'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'

/**
 * A heart for the playing song, toward song of the night. Not for your own
 * songs, and not for guests in rooms where they don't vote.
 */
export function HeartButton({ room, np, className }: { room: Room; np: NowPlaying; className?: string }) {
  const me = useMe()
  const queryClient = useQueryClient()
  const itemId = np.itemId ?? ''
  const query = heartsQuery(room.id, itemId)
  const hearts = useQuery({ ...query, enabled: !!itemId })
  const mine = !np.autopilot && np.requester?.id === me.id
  const allowed = !mine && (!me.guest || room.guests.canVote)
  const hearted = hearts.data?.userIds.includes(me.id) ?? false
  const count = hearts.data?.userIds.length ?? 0

  const toggle = useMutation({
    mutationFn: (on: boolean) => setHeart(room.id, itemId, on),
    onMutate: (on) => {
      const before = queryClient.getQueryData(query.queryKey)
      queryClient.setQueryData(query.queryKey, (h?: Hearts) =>
        h && { ...h, userIds: on ? [...h.userIds.filter((id) => id !== me.id), me.id] : h.userIds.filter((id) => id !== me.id) },
      )
      return { before }
    },
    onError: (e, _, ctx) => {
      queryClient.setQueryData(query.queryKey, ctx?.before)
      toast({ message: errorMessage(e), tone: 'error' })
    },
    onSuccess: (h) => queryClient.setQueryData(query.queryKey, h),
  })

  if (!itemId) return null
  const label = mine
    ? `${count} ${count === 1 ? 'heart' : 'hearts'} for your song`
    : hearted
      ? 'Take back your heart'
      : 'Heart this song for song of the night'

  return (
    <motion.button
      type="button"
      whileTap={allowed ? { scale: 0.85 } : undefined}
      disabled={!allowed || toggle.isPending}
      aria-pressed={allowed ? hearted : undefined}
      aria-label={label}
      title={label}
      onClick={() => {
        tap()
        toggle.mutate(!hearted)
      }}
      className={cn(
        'relative inline-flex h-9 items-center gap-1.5 rounded-full px-3 text-sm font-medium tabular-nums transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-default',
        hearted ? 'bg-rose-500/15 text-rose-500' : 'bg-muted/70 text-muted-foreground hover:text-foreground disabled:hover:text-muted-foreground',
        className,
      )}
    >
      <motion.span key={String(hearted)} initial={{ scale: hearted ? 0.4 : 1 }} animate={{ scale: 1 }} transition={spring}>
        <Heart className={cn('size-4', hearted && 'fill-current')} />
      </motion.span>
      <AnimatePresence mode="popLayout" initial={false}>
        {count > 0 && (
          <motion.span key={count} initial={{ y: 8, opacity: 0 }} animate={{ y: 0, opacity: 1 }} exit={{ y: -8, opacity: 0 }} transition={spring}>
            {count}
          </motion.span>
        )}
      </AnimatePresence>
    </motion.button>
  )
}
