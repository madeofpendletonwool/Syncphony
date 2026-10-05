import { AnimatePresence, motion } from 'motion/react'
import { easeOutExpo } from '@/lib/motion'
import { cn } from '@/lib/utils'

/**
 * The ambient layer behind everything: the current artwork, hugely blurred,
 * over a glow in the album accent. Crossfades when the song changes.
 */
export function AlbumBackdrop({ src, className }: { src?: string; className?: string }) {
  return (
    <div aria-hidden className={cn('pointer-events-none fixed inset-0 -z-10 overflow-hidden', className)}>
      <div className="absolute -top-1/4 left-1/2 size-[60rem] max-w-[200vw] -translate-x-1/2 rounded-full bg-[radial-gradient(closest-side,var(--glow),transparent)] blur-3xl transition-colors" />
      <div className="absolute inset-x-0 -top-24 h-[70vh] opacity-35 [mask-image:linear-gradient(to_bottom,black,transparent)] dark:opacity-60">
        <AnimatePresence>
          {src && (
            <motion.img
              key={src}
              src={src}
              alt=""
              initial={{ opacity: 0, scale: 1.1 }}
              animate={{ opacity: 1, scale: 1 }}
              exit={{ opacity: 0 }}
              transition={{ duration: 1.2, ease: easeOutExpo }}
              className="absolute inset-0 size-full object-cover blur-[90px] saturate-150"
            />
          )}
        </AnimatePresence>
      </div>
    </div>
  )
}
