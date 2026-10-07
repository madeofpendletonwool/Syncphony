import { GripVertical, Trash2, X } from 'lucide-react'
import { AnimatePresence, motion, Reorder, useDragControls, useMotionValue, useTransform } from 'motion/react'
import { useState } from 'react'
import { useQueueMove } from '@/hooks/use-queue-move'
import { useQueueRemoval } from '@/hooks/use-queue-removal'
import type { User } from '@/lib/now-playing'
import type { QueueItem } from '@/lib/playback'
import { QueueRow } from './queue-row'

// How far left a row must be swiped to remove it.
const SWIPE_REMOVE = 110

/** Your songs, in your lane's order: drag to reorder, swipe left to remove. */
export function MyLane({ roomId, items, me }: { roomId: string; items: QueueItem[]; me: User }) {
  const serverOrder = items.map((i) => i.id)
  // While dragging, or until the server confirms a change, show our own order.
  const [draft, setDraft] = useState<string[]>()
  const order = draft ?? serverOrder

  const byId = new Map(items.map((i) => [i.id, i]))

  const move = useQueueMove(roomId)

  const { remove } = useQueueRemoval(roomId)
  const removeItem = (item: QueueItem) => {
    // Gone at once; the server's queue catches up.
    setDraft(order.filter((id) => id !== item.id))
    remove.mutate({ item }, { onSettled: () => setDraft(undefined) })
  }

  const dropped = (id: string) => {
    const from = serverOrder.indexOf(id)
    const to = order.indexOf(id)
    if (from !== to && to >= 0) move.mutate({ itemId: id, position: to }, { onSettled: () => setDraft(undefined) })
    else setDraft(undefined)
  }

  if (order.length === 0) {
    return <p className="px-3 py-4 text-sm text-muted-foreground">Nothing waiting. Add songs from Search.</p>
  }

  return (
    <Reorder.Group axis="y" values={order} onReorder={setDraft} className="flex flex-col gap-1">
      <AnimatePresence initial={false}>
        {order.map((id) => {
          const item = byId.get(id)
          if (!item) return null
          return (
            <LaneRow
              key={id}
              roomId={roomId}
              item={item}
              me={me}
              onDrop={() => dropped(id)}
              onRemove={() => removeItem(item)}
            />
          )
        })}
      </AnimatePresence>
    </Reorder.Group>
  )
}

function LaneRow({
  roomId,
  item,
  me,
  onDrop,
  onRemove,
}: {
  roomId: string
  item: QueueItem
  me: User
  onDrop: () => void
  onRemove: () => void
}) {
  const controls = useDragControls()
  const x = useMotionValue(0)
  // The red "remove" layer fades in as the row slides left.
  const reveal = useTransform(x, [-SWIPE_REMOVE, -24, 0], [1, 0.4, 0])

  return (
    <Reorder.Item
      value={item.id}
      dragListener={false}
      dragControls={controls}
      onDragEnd={onDrop}
      initial={{ opacity: 0, height: 0 }}
      animate={{ opacity: 1, height: 'auto' }}
      exit={{ opacity: 0, height: 0, transition: { duration: 0.25 } }}
      whileDrag={{ scale: 1.02, zIndex: 10 }}
      className="relative"
    >
      <motion.div
        aria-hidden
        style={{ opacity: reveal }}
        className="absolute inset-0 flex items-center justify-end rounded-2xl bg-destructive/20 pr-5 text-destructive"
      >
        <Trash2 className="size-5" />
      </motion.div>
      <motion.div
        drag="x"
        dragDirectionLock
        dragConstraints={{ left: 0, right: 0 }}
        dragElastic={{ left: 0.7, right: 0.05 }}
        style={{ x }}
        onDragEnd={(_, info) => {
          if (info.offset.x < -SWIPE_REMOVE || info.velocity.x < -800) onRemove()
        }}
        className="glass relative touch-pan-y rounded-2xl"
      >
        <QueueRow
          roomId={roomId}
          item={item}
          user={me}
          hideAvatar
          leading={
            <button
              type="button"
              aria-label={`Drag to reorder ${item.track.title}`}
              onPointerDown={(e) => {
                e.preventDefault()
                controls.start(e)
              }}
              className="-ml-1 grid size-8 shrink-0 cursor-grab touch-none place-items-center rounded-lg text-muted-foreground outline-none active:cursor-grabbing focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <GripVertical className="size-4" />
            </button>
          }
          trailing={
            <button
              type="button"
              aria-label={`Remove ${item.track.title}`}
              onClick={onRemove}
              className="hidden size-8 shrink-0 place-items-center rounded-full text-muted-foreground outline-none hover:bg-destructive/15 hover:text-destructive focus-visible:grid focus-visible:ring-3 focus-visible:ring-ring/50 [@media(hover:hover)]:grid"
            >
              <X className="size-4" />
            </button>
          }
        />
      </motion.div>
    </Reorder.Item>
  )
}
