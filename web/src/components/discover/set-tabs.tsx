import { Link } from '@tanstack/react-router'
import { ListMusic, Waypoints } from 'lucide-react'
import { cn } from '@/lib/utils'

const tab =
  'flex items-center gap-1.5 rounded-full px-4 py-1.5 text-sm font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50'

/** A set's two views: its feed of songs, and the map of its artists. */
export function SetTabs({ setId, on, className }: { setId: string; on: 'feed' | 'map'; className?: string }) {
  return (
    <nav aria-label="Views" className={cn('glass inline-flex gap-1 rounded-full p-1', className)}>
      <Link
        to="/discover/$setId"
        params={{ setId }}
        replace
        aria-current={on === 'feed' ? 'page' : undefined}
        className={cn(tab, on === 'feed' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground')}
      >
        <ListMusic className="size-4" />
        Feed
      </Link>
      <Link
        to="/discover/map"
        search={{ set: setId }}
        replace
        aria-current={on === 'map' ? 'page' : undefined}
        className={cn(tab, on === 'map' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground')}
      >
        <Waypoints className="size-4" />
        Map
      </Link>
    </nav>
  )
}
