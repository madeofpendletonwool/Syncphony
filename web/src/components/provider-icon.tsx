import { AudioLines, FlaskConical, Music, Server } from 'lucide-react'
import { cn } from '@/lib/utils'

// Keyed by ProviderInfo.icon. Unknown providers get a generic note.
const icons: Record<string, { icon: typeof Music; color: string }> = {
  navidrome: { icon: Server, color: '#3b82f6' },
  spotify: { icon: AudioLines, color: '#1db954' },
  fake: { icon: FlaskConical, color: '#a855f7' },
}

/** A provider's badge: its glyph on a tile in its brand color. */
export function ProviderIcon({ icon, className }: { icon: string; className?: string }) {
  const { icon: Icon, color } = icons[icon] ?? { icon: Music, color: 'var(--primary)' }
  return (
    <span
      aria-hidden
      style={{ '--brand': color } as React.CSSProperties}
      className={cn(
        'grid size-11 shrink-0 place-items-center rounded-2xl bg-(--brand)/15 text-(--brand) dark:text-[color-mix(in_oklch,var(--brand),white_25%)]',
        className,
      )}
    >
      <Icon className="size-5" />
    </span>
  )
}
