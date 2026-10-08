import { AnimatePresence, motion } from 'motion/react'
import { useLoadedSrc } from '@/hooks/use-loaded-src'
import { easeOutExpo } from '@/lib/motion'
import { cn } from '@/lib/utils'

/**
 * The ambient layer behind everything: a slowly drifting mesh of the
 * album's palette, a glow in its accent, and the artwork itself, hugely
 * blurred. The palette colors are registered CSS properties, so the mesh
 * eases from one song's colors to the next; the artwork crossfades.
 */
export function AlbumBackdrop({ src: next, className }: { src?: string; className?: string }) {
  // Crossfade to art that's ready, not to an empty image.
  const src = useLoadedSrc(next)
  return (
    <div aria-hidden className={cn('pointer-events-none fixed inset-0 -z-10 overflow-hidden', className)}>
      <div className="absolute inset-0 opacity-30 dark:opacity-55">
        <Blob className="mesh-a -top-1/4 -left-1/4 size-[75vmax]" color="var(--pal-vibrant)" />
        <Blob className="mesh-b top-1/5 -right-1/3 size-[65vmax]" color="var(--pal-muted)" />
        <Blob className="mesh-c -bottom-1/3 left-1/6 size-[70vmax]" color="var(--pal-dominant)" />
      </div>
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

/** One soft blob of the mesh. Still when the user asks for less motion. */
function Blob({ className, color }: { className: string; color: string }) {
  return (
    <div
      style={{ backgroundImage: `radial-gradient(closest-side, ${color}, transparent)` }}
      className={cn('absolute rounded-full blur-3xl will-change-transform motion-reduce:animate-none', className)}
    />
  )
}
