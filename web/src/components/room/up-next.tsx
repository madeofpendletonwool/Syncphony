import { GripVertical } from 'lucide-react'
import { AnimatePresence, Reorder, useDragControls } from 'motion/react'
import { useState } from 'react'
import { useQueueMove } from '@/hooks/use-queue-move'
import { isMine } from '@/lib/autopilot'
import { spring } from '@/lib/motion'
import type { User } from '@/lib/now-playing'
import { lanePositionAt, type QueueItem } from '@/lib/playback'
import { NotThisOne } from './autopilot-badge'
import { QueueRow } from './queue-row'
import { RemoveTheirs } from './remove-theirs'

/**
 * The queue in fair play order: everyone's turns interleaved. Your own
 * songs carry a drag handle — dropping one moves it within your lane,
 * and the fair order settles around it when the server answers. Other
 * people's songs can't be dragged, only the room's owner can remove them.
 */
export function UpNext({
  roomId,
  items,
  limit,
  me,
  owner,
  userById,
  byline,
}: {
  roomId: string
  /** The whole up-next list in fair order; only the first `limit` rows show. */
  items: QueueItem[]
  limit: number
  me: string
  owner?: boolean
  userById: (id: string) => User | undefined
  byline?: boolean
}) {
  const serverOrder = items.map((i) => i.id)
  // While dragging, or until the server confirms the drop, show our own order.
  const [draft, setDraft] = useState<string[]>()
  const order = draft ?? serverOrder
  const byId = new Map(items.map((i) => [i.id, i]))
  const mineIds = new Set(items.filter((i) => isMine(i, me) && i.state === 'queued').map((i) => i.id))

  const move = useQueueMove(roomId)

  const dropped = (id: string) => {
    if (draft) {
      const to = lanePositionAt(draft, id, mineIds)
      if (to !== lanePositionAt(serverOrder, id, mineIds)) {
        move.mutate({ itemId: id, position: to }, { onSettled: () => setDraft(undefined) })
        return
      }
    }
    setDraft(undefined)
  }

  const visible = order.slice(0, limit)
  const rest = order.slice(limit)

  return (
    <Reorder.Group
      as="ol"
      axis="y"
      values={visible}
      onReorder={(next) => setDraft([...next, ...rest])}
      className="glass flex flex-col rounded-3xl p-1.5"
    >
      <AnimatePresence initial={false}>
        {visible.map((id, i) => {
          const item = byId.get(id)
          if (!item) return null
          return (
            <UpNextRow
              key={id}
              roomId={roomId}
              item={item}
              index={i + 1}
              mine={mineIds.has(id)}
              me={me}
              owner={owner}
              user={userById(item.addedBy)}
              byline={byline}
              onDrop={mineIds.has(id) ? () => dropped(id) : undefined}
            />
          )
        })}
      </AnimatePresence>
      {rest.length > 0 && <li className="px-4 py-2.5 text-sm text-muted-foreground">and {rest.length} more</li>}
    </Reorder.Group>
  )
}

function UpNextRow({
  roomId,
  item,
  index,
  mine,
  me,
  owner,
  user,
  byline,
  onDrop,
}: {
  roomId: string
  item: QueueItem
  index: number
  mine: boolean
  me: string
  owner?: boolean
  user?: User
  byline?: boolean
  onDrop?: () => void
}) {
  const controls = useDragControls()
  return (
    <Reorder.Item
      value={item.id}
      dragListener={false}
      dragControls={controls}
      onDragEnd={onDrop}
      initial={{ opacity: 0, scale: 0.97 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, scale: 0.97 }}
      transition={spring}
      whileDrag={onDrop ? { scale: 1.02, zIndex: 10 } : undefined}
    >
      <QueueRow
        roomId={roomId}
        item={item}
        user={user}
        mine={mine}
        byline={byline || !!item.autopilot}
        leading={<span className="w-5 shrink-0 text-center text-sm text-muted-foreground tabular-nums">{index}</span>}
        trailing={
          item.autopilot ? (
            <NotThisOne roomId={roomId} item={item} />
          ) : onDrop ? (
            <button
              type="button"
              aria-label={`Drag to reorder ${item.track.title}`}
              onPointerDown={(e) => {
                e.preventDefault()
                controls.start(e)
              }}
              className="-mr-1 grid size-8 shrink-0 cursor-grab touch-none place-items-center rounded-lg text-muted-foreground outline-none active:cursor-grabbing focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <GripVertical className="size-4" />
            </button>
          ) : (
            owner && item.addedBy !== me && item.state === 'queued' && (
              <RemoveTheirs roomId={roomId} item={item} owner={user} />
            )
          )
        }
      />
    </Reorder.Item>
  )
}
