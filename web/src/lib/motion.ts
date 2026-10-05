import type { Transition, Variants } from 'motion/react'

// Shared Motion presets so the app moves with one personality: quick,
// springy, never bouncy enough to feel toy-like.

export const spring: Transition = { type: 'spring', stiffness: 420, damping: 36, mass: 0.9 }
export const softSpring: Transition = { type: 'spring', stiffness: 260, damping: 30 }
export const easeOutExpo = [0.16, 1, 0.3, 1] as const

export const fadeUp: Variants = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: 0.45, ease: easeOutExpo } },
}

export const stagger: Variants = {
  hidden: {},
  show: { transition: { staggerChildren: 0.05 } },
}
