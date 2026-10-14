import { MessageCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useChatUnread } from '@/hooks/use-chat'
import { useMe } from '@/lib/auth'
import { chatOpen, unreadLabel } from '@/lib/chat'
import { cn } from '@/lib/utils'

/** Opens the room's chat, with how many messages you haven't read. */
export function ChatButton({ roomId, className }: { roomId: string; className?: string }) {
  const me = useMe()
  const unread = useChatUnread(roomId, me.id)
  return (
    <Button
      variant="glass"
      size="sm"
      onClick={() => chatOpen.set(true)}
      aria-label={unread > 0 ? `Chat, ${unread} unread` : 'Chat'}
      className={cn('relative', className)}
    >
      <MessageCircle data-icon="inline-start" />
      Chat
      {unread > 0 && <UnreadBadge count={unread} className="-top-1.5 -right-1.5" />}
    </Button>
  )
}

/** A small count in the corner of whatever it's in. */
export function UnreadBadge({ count, className }: { count: number; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn(
        'absolute flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[0.6rem] leading-none font-semibold text-primary-foreground tabular-nums',
        className,
      )}
    >
      {unreadLabel(count)}
    </span>
  )
}
