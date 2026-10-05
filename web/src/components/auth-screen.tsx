import { motion } from 'motion/react'
import type { ReactNode } from 'react'
import { AlbumBackdrop } from '@/components/shell/album-backdrop'
import { fadeUp, stagger } from '@/lib/motion'

/** Full-screen frame for signing in and signing up: no nav, no player. */
export function AuthScreen({ title, subtitle, children }: { title: string; subtitle?: ReactNode; children: ReactNode }) {
  return (
    <div className="relative isolate flex min-h-dvh flex-col">
      <AlbumBackdrop />
      <motion.main
        variants={stagger}
        initial="hidden"
        animate="show"
        className="pt-safe pb-safe mx-auto flex w-full max-w-sm flex-1 flex-col justify-center px-gutter py-10"
      >
        <motion.div variants={fadeUp} className="mb-8 flex flex-col items-center text-center">
          <BrandMark className="mb-6 size-14" />
          <h1 className="text-title">{title}</h1>
          {subtitle && <p className="mt-2 text-muted-foreground">{subtitle}</p>}
        </motion.div>
        <motion.div variants={fadeUp}>{children}</motion.div>
      </motion.main>
    </div>
  )
}

/** The Syncphony equalizer mark, tinted by the album accent. */
export function BrandMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden className={className}>
      <rect width="32" height="32" rx="9" className="fill-primary/15" />
      <g className="fill-primary">
        <rect x="6" y="13" width="3" height="6" rx="1.5" />
        <rect x="11" y="9" width="3" height="14" rx="1.5" />
        <rect x="16" y="6" width="3" height="20" rx="1.5" />
        <rect x="21" y="11" width="3" height="10" rx="1.5" />
      </g>
    </svg>
  )
}

/** "or" between the primary sign-in method and the fallback. */
export function OrDivider() {
  return (
    <div className="my-5 flex items-center gap-3 text-caption text-muted-foreground">
      <span className="h-px flex-1 bg-border" />
      or
      <span className="h-px flex-1 bg-border" />
    </div>
  )
}
