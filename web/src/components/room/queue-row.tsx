import type { ReactNode } from 'react'
import { Artwork } from '@/components/artwork'
import { ServiceGlyph } from '@/components/service-tag'
import { AutopilotMark } from '@/components/room/autopilot-badge'
import { UserAvatar } from '@/components/user-avatar'
import { autopilotReason } from '@/lib/autopilot'
import { laneStyle } from '@/lib/lane'
import { queueArtworkUrl, type QueueItem } from '@/lib/playback'
import type { User } from '@/lib/now-playing'
import { cn } from '@/lib/utils'

/**
 * A queued song: artwork, title, and a stripe in the color of whoever added
 * it. Autopilot's songs get autopilot's mark instead of anyone's.
 */
export function QueueRow({
  roomId,
  item,
  user,
  leading,
  trailing,
  mine,
  hideAvatar,
  byline,
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
  /** Name who added it under the title, for lists without lane context. */
  byline?: boolean
  className?: string
}) {
  const autopilot = item.autopilot
  if (autopilot) {
    user = undefined
    mine = false
  }
  return (
    <div
      style={laneStyle(user?.color)}
      className={cn(
        'relative flex items-center gap-3 rounded-2xl py-2 pr-2 pl-3',
        'before:absolute before:inset-y-3 before:left-0 before:w-1 before:rounded-full before:bg-(--lane)',
        autopilot && 'before:bg-primary/50',
        mine && 'bg-(--lane)/8',
        className,
      )}
    >
      {leading}
      <Artwork src={queueArtworkUrl(roomId, item, 120)} className="size-11 rounded-lg shadow-none" />
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium">{item.track.title}</p>
        <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
          <ServiceGlyph provider={item.track.provider} />
          <span className="truncate">
            {item.track.artists.join(', ')}
            {byline && user && (
              <>
                {' · '}
                <span className="text-(--lane) dark:text-[color-mix(in_oklch,var(--lane),white_30%)]">{user.displayName}</span>
              </>
            )}
            {byline && autopilot && (
              <>
                {' · '}
                <span className="text-primary">{autopilotReason(autopilot)}</span>
              </>
            )}
          </span>
        </p>
      </div>
      {user && !hideAvatar && <UserAvatar user={user} className="size-6 text-[0.6rem]" />}
      {autopilot && <AutopilotMark />}
      {trailing}
    </div>
  )
}
