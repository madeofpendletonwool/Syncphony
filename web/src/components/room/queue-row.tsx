import { Play } from 'lucide-react'
import { useEffect, useRef, useState, type ReactNode } from 'react'
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
  onPlay,
  playLabel = 'Play now',
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
  /** Makes the artwork a play button: hover (or tap once on touch) shows it. */
  onPlay?: () => void
  playLabel?: string
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
      {onPlay ? (
        <PlayArt label={`${playLabel}: ${item.track.title}`} onPlay={onPlay}>
          <Artwork src={queueArtworkUrl(roomId, item, 120)} className="size-11 rounded-lg shadow-none" />
        </PlayArt>
      ) : (
        <Artwork src={queueArtworkUrl(roomId, item, 120)} className="size-11 rounded-lg shadow-none" />
      )}
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
          {item.heldForGame && <span className="shrink-0 text-caption text-primary">· In a game</span>}
        </p>
      </div>
      {user && !hideAvatar && <UserAvatar user={user} className="size-6 text-[0.6rem]" />}
      {autopilot && <AutopilotMark />}
      {trailing}
    </div>
  )
}

// How long a first tap on touch keeps the play button up.
const ARMED_FOR = 3000

/**
 * Artwork that plays its song. With a mouse, hovering shows the button. On
 * touch there's no hover, so the first tap shows it and a second plays:
 * one stray tap shouldn't skip the song the room is listening to.
 */
function PlayArt({ label, onPlay, children }: { label: string; onPlay: () => void; children: ReactNode }) {
  const [armed, setArmed] = useState(false)
  const pointer = useRef('mouse')
  useEffect(() => {
    if (!armed) return
    const t = window.setTimeout(() => setArmed(false), ARMED_FOR)
    return () => window.clearTimeout(t)
  }, [armed])
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      data-armed={armed || undefined}
      onPointerDown={(e) => {
        pointer.current = e.pointerType
      }}
      onClick={(e) => {
        e.stopPropagation()
        if (pointer.current === 'touch' && !armed) {
          setArmed(true)
          return
        }
        setArmed(false)
        onPlay()
      }}
      className="group/play relative shrink-0 rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      {children}
      <span
        aria-hidden
        className="absolute inset-0 grid place-items-center rounded-lg bg-black/50 text-white opacity-0 transition-opacity group-hover/play:opacity-100 group-focus-visible/play:opacity-100 group-data-armed/play:opacity-100"
      >
        <Play className="size-5 fill-current" />
      </span>
    </button>
  )
}
