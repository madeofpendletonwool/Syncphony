import { providerLook } from '@/lib/services'
import { cn } from '@/lib/utils'

/** A provider's badge: its glyph on a tile in its brand color. */
export function ProviderIcon({ icon, className }: { icon: string; className?: string }) {
  const { icon: Icon, color } = providerLook(icon)
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
