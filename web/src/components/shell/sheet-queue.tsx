import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, type Ref } from 'react'
import { Link } from '@tanstack/react-router'
import { QueueRow } from '@/components/room/queue-row'
import { RequeueButton } from '@/components/room/requeue-button'
import { useMe } from '@/lib/auth'
import { historyQuery, type QueueItem } from '@/lib/playback'
import { queueQuery } from '@/lib/room'
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
  const queryClient = useQueryClient()
  const queue = useQuery(queueQuery(roomId))
  const history = useQuery(historyQuery(roomId))
  const users = useQuery(usersQuery)
  const userById = (id: string) => users.data?.find((u) => u.id === id)

  // A new song means the last one just joined the history.
  useEffect(() => {
    void queryClient.invalidateQueries({ queryKey: historyQuery(roomId).queryKey })
  }, [queryClient, roomId, itemId])

  const byId = new Map(queue.data?.items.map((i) => [i.id, i]))
  const upNext = (queue.data?.upNext ?? []).map((id) => byId.get(id)).filter((i): i is QueueItem => !!i)
  const played = history.data ?? []

  return (
    <section ref={ref} aria-label="Queue" className={cn('flex flex-col gap-6 px-gutter pt-2 pb-safe', className)}>
      <div>
        <Heading title="Up next" count={upNext.length} />
        {upNext.length === 0 ? (
          <p className="px-1 text-sm text-muted-foreground">Nothing waiting. Add a song to your lane.</p>
        ) : (
          <ol className="glass flex flex-col rounded-3xl p-1.5">
            {upNext.slice(0, SHOWN).map((item, i) => (
              <li key={item.id}>
                <QueueRow
                  roomId={roomId}
                  item={item}
                  user={userById(item.addedBy)}
                  mine={item.addedBy === me.id}
                  byline
                  leading={<span className="w-5 shrink-0 text-center text-sm text-muted-foreground tabular-nums">{i + 1}</span>}
                />
              </li>
            ))}
            {upNext.length > SHOWN && (
              <li className="px-4 py-2.5 text-sm text-muted-foreground">and {upNext.length - SHOWN} more</li>
            )}
          </ol>
        )}
      </div>

      {played.length > 0 && (
        <div className="pb-8">
          <div className="mb-2 flex items-baseline justify-between px-1">
            <h2 className="text-headline">Recently played</h2>
            <Link to="/history" className="text-sm font-medium text-primary">
              See all
            </Link>
          </div>
          <ol className="glass flex flex-col rounded-3xl p-1.5">
            {played.map((p) => (
              <li key={`${p.item.id}-${p.startedAt}`}>
                <QueueRow
                  roomId={roomId}
                  item={p.item}
                  user={userById(p.item.addedBy)}
                  byline
                  className="opacity-75"
                  trailing={
                    <>
                      {p.endReason !== 'finished' && (
                        <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-caption text-muted-foreground">
                          {p.endReason === 'error' ? "Couldn't play" : p.endReason === 'skipped' ? 'Skipped' : 'Removed'}
                        </span>
                      )}
                      <RequeueButton item={p.item} />
                    </>
                  }
                />
              </li>
            ))}
          </ol>
        </div>
      )}
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
