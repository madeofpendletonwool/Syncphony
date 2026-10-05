import { useService } from '@/lib/services'
import { cn } from '@/lib/utils'

/** Where a song comes from: the service's glyph and name in its brand color. */
export function ServiceTag({ provider, className }: { provider: string; className?: string }) {
  const { name, icon: Icon, color } = useService(provider)
  return (
    <span
      style={{ '--brand': color } as React.CSSProperties}
      className={cn(
        'inline-flex w-fit items-center gap-1.5 rounded-full bg-(--brand)/15 py-0.5 pr-2.5 pl-2 text-xs font-medium text-(--brand) dark:text-[color-mix(in_oklch,var(--brand),white_30%)]',
        className,
      )}
    >
      <Icon className="size-3.5" />
      {name}
    </span>
  )
}

/** Just the service's glyph, for dense rows. */
export function ServiceGlyph({ provider, className }: { provider: string; className?: string }) {
  const { name, icon: Icon, color } = useService(provider)
  return (
    <Icon
      role="img"
      aria-label={name}
      style={{ color }}
      className={cn('size-3.5 shrink-0 dark:brightness-125', className)}
    />
  )
}
