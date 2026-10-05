import type { ReactNode } from 'react'
import { Artwork } from '@/components/artwork'
import { UserAvatar } from '@/components/user-avatar'
import { laneStyle } from '@/lib/lane'
import { queueArtworkUrl, type QueueItem } from '@/lib/playback'
import type { User } from '@/lib/now-playing'
import { cn } from '@/lib/utils'

/** A queued song: artwork, title, and a stripe in the color of whoever added it. */
export function QueueRow({
  roomId,
  item,
  user,
  leading,
  trailing,
  mine,
  hideAvatar,
  className,
}: {
  roomId: string
  item: QueueItem
  user?: Pick<User, 'displayName' | 'color' | 'avatar'>
  leading?: ReactNode
  trailing?: ReactNode
  /** Highlights your own songs. */
  mine?: boolean
  /** Keep the lane stripe but skip the avatar (in a lane that's all one person's). */
  hideAvatar?: boolean
  className?: string
}) {
  return (
    <div
      style={laneStyle(user?.color)}
      className={cn(
        'relative flex items-center gap-3 rounded-2xl py-2 pr-2 pl-3',
        'before:absolute before:inset-y-3 before:left-0 before:w-1 before:rounded-full before:bg-(--lane)',
        mine && 'bg-(--lane)/8',
        className,
      )}
    >
      {leading}
      <Artwork src={queueArtworkUrl(roomId, item, 120)} className="size-11 rounded-lg shadow-none" />
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium">{item.track.title}</p>
        <p className="truncate text-sm text-muted-foreground">{item.track.artists.join(', ')}</p>
      </div>
      {user && !hideAvatar && <UserAvatar user={user} className="size-6 text-[0.6rem]" />}
      {trailing}
    </div>
  )
}
