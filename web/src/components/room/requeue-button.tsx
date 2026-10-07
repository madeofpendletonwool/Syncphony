import { useQuery } from '@tanstack/react-query'
import { AddButton } from '@/components/add-button'
import { laneTrackOf, useAddToLane } from '@/hooks/use-add-to-lane'
import { useMe } from '@/lib/auth'
import { queuedCopy } from '@/lib/duplicates'
import type { QueueItem } from '@/lib/playback'
import { usableLinksQuery } from '@/lib/services'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'

/**
 * Puts a song the room played back in your lane. Only shown when you can:
 * it came from a link you can use (yours, or shared), or the room lets
 * people borrow songs from each other's services.
 *
 * If the song is already waiting or playing, it asks first. In a room that
 * doesn't repeat songs, it just says so: the server would refuse it.
 */
export function RequeueButton({ item, className = 'size-9' }: { item: QueueItem; className?: string }) {
  const me = useMe()
  const links = useQuery(usableLinksQuery)
  const users = useQuery(usersQuery)
  const { add, status, room, queue } = useAddToLane()
  const linkId = item.track.linkId
  const usable = links.data?.some((l) => l.id === linkId && l.status === 'ok')
  if (!linkId || !(usable || room?.matching.borrow)) return null
  const track = laneTrackOf(item, linkId)

  const onAdd = () => {
    const copy = queue && queuedCopy(queue.items, item.track)
    if (!copy) return add([track])
    const title = item.track.title
    const where =
      copy.state === 'playing'
        ? 'playing now'
        : copy.autopilot
          ? 'already up next'
          : copy.addedBy === me.id
            ? 'already in your lane'
            : `already in ${users.data?.find((u) => u.id === copy.addedBy)?.displayName ?? 'someone'}’s lane`
    const repeats = (room?.fairness.repeatWindowMinutes ?? 0) === 0
    toast(
      { message: `“${title}” is ${where}`, action: repeats ? { label: 'Add anyway', onClick: () => add([track]) } : undefined },
      5000,
    )
  }

  return <AddButton status={status(track)} onAdd={onAdd} title={item.track.title} className={className} />
}
