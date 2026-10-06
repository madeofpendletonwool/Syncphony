import { cn } from 'cn'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { motion, useReducedMotion } from 'motion/react'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { stagger } from '@/lib/motion'

/**
 * A sideways-scrolling row that bleeds to the screen edges. Fingers and
 * trackpads swipe it; mouse users get a thin scrollbar and, on hover,
 * arrows that page through it (MAD-738).
 */
export function Shelf({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const reduced = useReducedMotion()
  const [ends, setEnds] = useState({ start: true, end: true })

  const measure = useCallback(() => {
    const el = ref.current
    if (!el) return
    // A pixel of slack: scrollLeft is fractional on zoomed or HiDPI screens.
    const start = el.scrollLeft <= 1
    const end = el.scrollLeft >= el.scrollWidth - el.clientWidth - 1
    setEnds((e) => (e.start === start && e.end === end ? e : { start, end }))
  }, [])

  useEffect(() => {
    const el = ref.current
    if (!el) return
    el.addEventListener('scroll', measure, { passive: true })
    const resize = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(measure)
    resize?.observe(el)
    return () => {
      el.removeEventListener('scroll', measure)
      resize?.disconnect()
    }
  }, [measure])

  // New cards change the row's width without resizing the row itself.
  useEffect(measure, [measure, children])

  // Most of a screen at a time, so the last card in view stays in sight;
  // scroll snapping then lines the row up on a card.
  const page = (dir: 1 | -1) => {
    const el = ref.current
    el?.scrollBy({ left: dir * el.clientWidth * 0.8, behavior: reduced ? 'auto' : 'smooth' })
  }

  return (
    <div className="group/shelf relative -mx-gutter">
      <motion.div
        ref={ref}
        variants={stagger}
        initial="hidden"
        animate="show"
        className="flex snap-x snap-mandatory scroll-px-gutter gap-4 overflow-x-auto px-gutter pb-2 shelf-scrollbar"
      >
        {children}
      </motion.div>
      {!ends.start && <Arrow side="start" onClick={() => page(-1)} />}
      {!ends.end && <Arrow side="end" onClick={() => page(1)} />}
    </div>
  )
}

// Mouse only: touch screens swipe, and keyboard users tab through the cards,
// which scrolls them into view.
function Arrow({ side, onClick }: { side: 'start' | 'end'; onClick: () => void }) {
  return (
    <Button
      variant="glass"
      size="icon"
      tabIndex={-1}
      aria-label={side === 'start' ? 'Scroll back' : 'Scroll forward'}
      onClick={onClick}
      className={cn(
        'absolute top-1/2 hidden -translate-y-1/2 opacity-0 shadow-lg group-hover/shelf:opacity-100 pointer-fine:inline-flex',
        side === 'start' ? 'left-2' : 'right-2',
      )}
    >
      {side === 'start' ? <ChevronLeft className="size-5" /> : <ChevronRight className="size-5" />}
    </Button>
  )
}
