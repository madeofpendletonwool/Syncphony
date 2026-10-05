import { motion } from 'motion/react'
import { cn } from '@/lib/utils'

/** An on/off switch. */
export function Switch({ checked, onChange, label }: { checked: boolean; onChange: (on: boolean) => void; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative h-7 w-12 shrink-0 rounded-full transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
        checked ? 'bg-primary' : 'bg-muted',
      )}
    >
      <motion.span
        layout
        transition={{ type: 'spring', stiffness: 600, damping: 35 }}
        className={cn('absolute top-1 size-5 rounded-full bg-white shadow', checked ? 'right-1' : 'left-1')}
      />
    </button>
  )
}
