import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { initials, laneStyle } from '@/lib/lane'
import type { User } from '@/lib/now-playing'
import { cn } from '@/lib/utils'

type Props = {
  user: Pick<User, 'displayName' | 'color' | 'avatar'>
  /** Draws a ring in the user's lane color. */
  ring?: boolean
  className?: string
}

/** A member's avatar, falling back to initials on their lane color. */
export function UserAvatar({ user, ring, className }: Props) {
  return (
    <Avatar
      style={laneStyle(user.color)}
      className={cn(ring && 'ring-2 ring-(--lane) ring-offset-2 ring-offset-background', className)}
    >
      {user.avatar && <AvatarImage src={user.avatar} alt="" />}
      <AvatarFallback className="bg-(--lane) text-white" aria-label={user.displayName}>
        {initials(user.displayName)}
      </AvatarFallback>
    </Avatar>
  )
}

/** A small dot in a user's lane color, for dense lists. */
export function LaneDot({ color, className }: { color: string; className?: string }) {
  return (
    <span
      aria-hidden
      style={laneStyle(color)}
      className={cn('inline-block size-2 shrink-0 rounded-full bg-(--lane) shadow-[0_0_8px_var(--lane)]', className)}
    />
  )
}
