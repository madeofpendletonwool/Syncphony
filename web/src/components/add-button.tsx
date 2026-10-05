import { Check, Plus } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import type { LaneStatus } from '@/hooks/use-add-to-lane'
import { spring } from '@/lib/motion'
import { cn } from '@/lib/utils'

/**
 * One-tap "add to my lane". Flips to a check the moment it's tapped; the
 * request catches up behind it.
 */
export function AddButton({
  status,
  onAdd,
  title,
  className,
}: {
  status: LaneStatus
  onAdd: () => void
  title: string
  className?: string
}) {
  const done = status !== 'idle'
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation()
        if (!done) onAdd()
      }}
      aria-label={done ? `${title} is in your lane` : `Add ${title} to your lane`}
      aria-disabled={done || undefined}
      className={cn(
        'relative grid size-10 shrink-0 place-items-center rounded-full transition-colors duration-300 outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
        done ? 'bg-primary text-primary-foreground' : 'bg-primary/12 text-primary hover:bg-primary/20 active:scale-95',
        className,
      )}
    >
      <AnimatePresence mode="popLayout" initial={false}>
        <motion.span
          key={done ? 'done' : 'add'}
          initial={{ scale: 0.4, rotate: done ? -90 : 90, opacity: 0 }}
          animate={{ scale: 1, rotate: 0, opacity: 1 }}
          exit={{ scale: 0.4, opacity: 0 }}
          transition={spring}
          className={cn(status === 'adding' && 'animate-pulse')}
        >
          {done ? <Check className="size-5" strokeWidth={2.5} /> : <Plus className="size-5" strokeWidth={2.25} />}
        </motion.span>
      </AnimatePresence>
    </button>
  )
}
