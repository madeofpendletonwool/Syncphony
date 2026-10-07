import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useEffect } from 'react'
import { QueueRow } from '@/components/room/queue-row'
import { RequeueButton } from '@/components/room/requeue-button'
import { historyQuery } from '@/lib/playback'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

/**
 * What the room just played, newest first, with who queued each song and a
 * one-tap "add to my lane". `itemId` is the playing item: when it changes,
 * the last song just joined the history.
 */
export function RecentlyPlayed({ roomId, itemId, limit, className }: { roomId: string; itemId?: string; limit?: number; className?: string }) {
  const queryClient = useQueryClient()
  const history = useQuery(historyQuery(roomId))
  const users = useQuery(usersQuery)
  const userById = (id: string) => users.data?.find((u) => u.id === id)

  useEffect(() => {
    void queryClient.invalidateQueries({ queryKey: historyQuery(roomId).queryKey })
  }, [queryClient, roomId, itemId])

  const played = (history.data ?? []).slice(0, limit)
  if (played.length === 0) return null

  return (
    <section className={className}>
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
              className={cn(p.endReason !== 'finished' && 'opacity-75')}
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
    </section>
  )
}
