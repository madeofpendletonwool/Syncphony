import { Link, useRouterState } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { CircleUserRound, Compass, Disc3, Library, Search } from 'lucide-react'
import { motion } from 'motion/react'
import { UnreadBadge } from '@/components/chat/chat-button'
import { useChatUnread } from '@/hooks/use-chat'
import { meQuery } from '@/lib/auth'
import { useCurrentRoom } from '@/lib/room'
import { spring } from '@/lib/motion'
import { cn } from '@/lib/utils'

const tabs = [
  { to: '/room', label: 'Room', icon: Disc3, also: ['/history'] },
  { to: '/search', label: 'Search', icon: Search, also: ['/album', '/artist'] },
  // Personal discovery (ADR 0017): members only.
  { to: '/discover', label: 'Discover', icon: Compass, also: [], members: true },
  { to: '/library', label: 'Library', icon: Library, also: [] },
  { to: '/me', label: 'Me', icon: CircleUserRound, also: ['/settings'] },
] as const

const under = (pathname: string, base: string) => pathname === base || pathname.startsWith(`${base}/`)

export function BottomNav() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const me = useQuery(meQuery).data
  const guest = !!me?.guest
  const { room } = useCurrentRoom()
  // The room's unread chat shows on the Room tab, wherever you are.
  const unread = useChatUnread(room?.id, me?.id ?? '')
  const shown = tabs.filter((t) => !('members' in t) || !guest)

  return (
    <nav aria-label="Main" className="glass-strong h-nav rounded-3xl p-1.5 shadow-float">
      <ul className={cn('grid h-full gap-1', shown.length === 5 ? 'grid-cols-5' : 'grid-cols-4')}>
        {shown.map(({ to, label, icon: Icon, also }) => {
          const active = [to, ...also].some((base) => under(pathname, base))
          return (
            <li key={to} className="relative">
              {active && (
                <motion.span
                  layoutId="nav-indicator"
                  transition={spring}
                  className="absolute inset-0 rounded-[1.1rem] bg-primary/15"
                />
              )}
              <Link
                to={to}
                aria-current={active ? 'page' : undefined}
                aria-label={to === '/room' && unread > 0 ? `${label}, ${unread} unread messages` : undefined}
                className={cn(
                  'relative flex h-full flex-col items-center justify-center gap-0.5 rounded-[1.1rem] text-[0.7rem] font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
                  active ? 'text-primary' : 'text-muted-foreground hover:text-foreground',
                )}
              >
                <motion.span animate={{ scale: active ? 1.08 : 1 }} transition={spring} className="relative">
                  <Icon className="size-[1.375rem]" strokeWidth={active ? 2.25 : 1.75} />
                  {to === '/room' && unread > 0 && <UnreadBadge count={unread} className="-top-1 -right-2" />}
                </motion.span>
                {label}
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}
