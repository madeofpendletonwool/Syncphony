import { CircleAlert, CircleCheck, Info } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import type { ReactNode } from 'react'
import { easeOutExpo } from '@/lib/motion'
import { cn } from '@/lib/utils'

const tones = {
  error: { icon: CircleAlert, className: 'bg-destructive/12 text-destructive' },
  success: { icon: CircleCheck, className: 'bg-success/12 text-success' },
  info: { icon: Info, className: 'bg-primary/12 text-primary' },
} as const

/**
 * An inline status line that animates in and out. Pass `children` as
 * null/undefined to hide it.
 */
export function Notice({
  tone = 'error',
  children,
  className,
}: {
  tone?: keyof typeof tones
  children?: ReactNode
  className?: string
}) {
  const { icon: Icon, className: toneClass } = tones[tone]
  return (
    <AnimatePresence initial={false}>
      {children && (
        <motion.div
          initial={{ opacity: 0, height: 0 }}
          animate={{ opacity: 1, height: 'auto' }}
          exit={{ opacity: 0, height: 0 }}
          transition={{ duration: 0.3, ease: easeOutExpo }}
          className="overflow-hidden"
        >
          <p
            role={tone === 'error' ? 'alert' : 'status'}
            className={cn('flex items-start gap-2.5 rounded-2xl px-4 py-3 text-sm', toneClass, className)}
          >
            <Icon className="mt-0.5 size-4 shrink-0" />
            <span className="min-w-0">{children}</span>
          </p>
        </motion.div>
      )}
    </AnimatePresence>
  )
}
