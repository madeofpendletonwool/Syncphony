import { AnimatePresence, motion } from 'motion/react'
import { useStore } from '@/lib/store'
import { spring } from '@/lib/motion'
import { dismiss, toasts } from '@/lib/toast'
import { cn } from '@/lib/utils'

/** Shows toasts just above the player dock. */
export function Toaster() {
  const list = useStore(toasts)
  return (
    <div aria-live="polite" className="pointer-events-none flex flex-col items-center">
      <AnimatePresence mode="popLayout">
        {list.map((t) => (
          <motion.div
            key={t.id}
            layout
            initial={{ opacity: 0, y: 16, scale: 0.96 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 8, scale: 0.96 }}
            transition={spring}
            role={t.tone === 'error' ? 'alert' : 'status'}
            className={cn(
              'glass-strong pointer-events-auto flex max-w-full items-center gap-3 rounded-full py-2 pr-2 pl-4 text-sm shadow-float',
              t.tone === 'error' && 'text-destructive',
              !t.action && 'pr-4',
            )}
          >
            <span className="min-w-0 truncate">{t.message}</span>
            {t.action && (
              <button
                type="button"
                onClick={() => {
                  t.action?.onClick()
                  dismiss(t.id)
                }}
                className="shrink-0 rounded-full px-3 py-1 font-medium text-primary transition-colors outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                {t.action.label}
              </button>
            )}
          </motion.div>
        ))}
      </AnimatePresence>
    </div>
  )
}
