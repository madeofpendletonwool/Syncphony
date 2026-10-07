import { useQuery } from '@tanstack/react-query'
import type { Ref } from 'react'
import { RecentlyPlayed } from '@/components/room/recently-played'
import { UpNext } from '@/components/room/up-next'
import { useMe } from '@/lib/auth'
import type { QueueItem } from '@/lib/playback'
import { queueQuery, roomsQuery } from '@/lib/room'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

// How much of each list to show; the room screen has the rest.
const SHOWN = 20

/** The room's queue in the now-playing view: what's next, and what just played. */
export function SheetQueue({
  roomId,
  itemId,
  ref,
  className,
}: {
  roomId: string
  itemId?: string
  ref?: Ref<HTMLElement>
  className?: string
}) {
  const me = useMe()
  const queue = useQuery(queueQuery(roomId))
  const users = useQuery(usersQuery)
  const rooms = useQuery(roomsQuery)
  const owner = rooms.data?.find((r) => r.id === roomId)?.ownerId === me.id
  const userById = (id: string) => users.data?.find((u) => u.id === id)

  const byId = new Map(queue.data?.items.map((i) => [i.id, i]))
  const upNext = (queue.data?.upNext ?? []).map((id) => byId.get(id)).filter((i): i is QueueItem => !!i)

  return (
    <section ref={ref} aria-label="Queue" className={cn('flex flex-col gap-6 px-gutter pt-2 pb-safe', className)}>
      <div>
        <Heading title="Up next" count={upNext.length} />
        {upNext.length === 0 ? (
          <p className="px-1 text-sm text-muted-foreground">Nothing waiting. Add a song to your lane.</p>
        ) : (
          <UpNext roomId={roomId} items={upNext} limit={SHOWN} me={me.id} owner={owner} userById={userById} byline />
        )}
      </div>

      <RecentlyPlayed roomId={roomId} itemId={itemId} className="pb-8" />
    </section>
  )
}

function Heading({ title, count }: { title: string; count?: number }) {
  return (
    <h2 className="mb-2 px-1 text-headline">
      {title}
      {count !== undefined && count > 0 && <span className="ml-2 text-muted-foreground tabular-nums">{count}</span>}
    </h2>
  )
}
