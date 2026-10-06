import { motion } from 'motion/react'
import { useEffect, useRef, type ReactNode } from 'react'
import { stagger } from '@/lib/motion'

/** A sideways-scrolling row that bleeds to the screen edges. */
export function Shelf({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)

  // A mouse wheel only spins vertically and the scrollbar is hidden on
  // touch screens, so a shelf could not be scrolled on desktop at all
  // (MAD-738). Turn a vertical wheel over the shelf into sideways
  // scrolling, and hand the wheel back to the page once the row reaches
  // its end.
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      if (e.ctrlKey || e.deltaX !== 0 || e.deltaY === 0) return
      const delta = e.deltaMode === WheelEvent.DOM_DELTA_LINE ? e.deltaY * 33 : e.deltaY
      const max = el.scrollWidth - el.clientWidth
      const next = Math.min(Math.max(el.scrollLeft + delta, 0), max)
      if (max <= 0 || next === el.scrollLeft) return
      e.preventDefault()
      el.scrollLeft = next
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [])

  return (
    <motion.div
      ref={ref}
      variants={stagger}
      initial="hidden"
      animate="show"
      className="-mx-gutter flex snap-x snap-mandatory scroll-px-gutter gap-4 overflow-x-auto px-gutter pb-2 no-scrollbar"
    >
      {children}
    </motion.div>
  )
}
