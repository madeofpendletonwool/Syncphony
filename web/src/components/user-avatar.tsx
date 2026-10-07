import { createElement } from 'react'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { avatarIcon } from '@/lib/avatar'
import { initials, laneStyle } from '@/lib/lane'
import type { User } from '@/lib/now-playing'
import { cn } from '@/lib/utils'

type Props = {
  user: Pick<User, 'displayName' | 'color' | 'avatar'>
  /** Draws a ring in the user's lane color. */
  ring?: boolean
  /** Shows their name on hover. On by default. */
  tooltip?: boolean
  className?: string
}

/**
 * A member's avatar: their picture, or an icon or initials on their lane
 * color. Hovering shows their name.
 */
export function UserAvatar({ user, ring, tooltip = true, className }: Props) {
  const icon = avatarIcon(user.avatar)
  const image = user.avatar && !user.avatar.startsWith('icon:') ? user.avatar : undefined
  const avatar = (
    <Avatar
      role="img"
      aria-label={user.displayName}
      style={laneStyle(user.color)}
      className={cn(ring && 'ring-2 ring-(--lane) ring-offset-2 ring-offset-background', className)}
    >
      {image && <AvatarImage src={image} alt="" />}
      <AvatarFallback className="bg-(--lane) text-white" aria-hidden>
        {icon ? createElement(icon, { className: 'size-[55%]', strokeWidth: 2.25 }) : initials(user.displayName)}
      </AvatarFallback>
    </Avatar>
  )
  if (!tooltip) return avatar
  return (
    <Tooltip>
      <TooltipTrigger asChild>{avatar}</TooltipTrigger>
      <TooltipContent>{user.displayName}</TooltipContent>
    </Tooltip>
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
