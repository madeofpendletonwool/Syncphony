import { Link, useRouterState } from '@tanstack/react-router'
import { CircleUserRound, Disc3, Search } from 'lucide-react'
import { motion } from 'motion/react'
import { spring } from '@/lib/motion'
import { cn } from '@/lib/utils'

const tabs = [
  { to: '/room', label: 'Room', icon: Disc3 },
  { to: '/search', label: 'Search', icon: Search },
  { to: '/me', label: 'Me', icon: CircleUserRound },
] as const

export function BottomNav() {
  const pathname = useRouterState({ select: (s) => s.location.pathname })

  return (
    <nav aria-label="Main" className="glass-strong h-nav rounded-3xl p-1.5 shadow-float">
      <ul className="grid h-full grid-cols-3 gap-1">
        {tabs.map(({ to, label, icon: Icon }) => {
          const active = pathname === to || pathname.startsWith(`${to}/`)
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
                className={cn(
                  'relative flex h-full flex-col items-center justify-center gap-0.5 rounded-[1.1rem] text-[0.7rem] font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
                  active ? 'text-primary' : 'text-muted-foreground hover:text-foreground',
                )}
              >
                <motion.span animate={{ scale: active ? 1.08 : 1 }} transition={spring}>
                  <Icon className="size-[1.375rem]" strokeWidth={active ? 2.25 : 1.75} />
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
