import { X } from 'lucide-react'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Button } from '@/components/ui/button'
import { useWakeLock } from '@/hooks/use-wake-lock'
import { beatSettings, lightsOpen, onBeatFrame } from '@/lib/beat'
import { usePlayer } from '@/lib/now-playing'
import { readColors } from '@/lib/scenes'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'

/**
 * Lights mode (MAD-781): the phone becomes a light. The whole screen is
 * a wash of the song's colors that flashes on each downbeat. Every phone
 * reads the same synced playback clock and the same beat map, so a room
 * of them flashes together, and they change color together, bar by bar,
 * like phones at a concert.
 *
 * Flashes are the downbeats (once a bar, so never more than about one a
 * second), with the engine's own capped beat pulse riding faintly under
 * them. With reduced motion, or the visuals off, it holds a steady glow.
 */
export function LightsMode() {
  const open = useStore(lightsOpen)
  return createPortal(<AnimatePresence>{open && <Lights />}</AnimatePresence>, document.body)
}

/** How bright the wash sits between flashes, and while nothing plays. */
const IDLE = 0.12

function Lights() {
  const { nowPlaying: np } = usePlayer()
  const { level } = useStore(beatSettings)
  const still = !!useReducedMotion() || level === 'off'
  const ref = useRef<HTMLDivElement>(null)
  const wash = useRef<HTMLDivElement>(null)
  const [idle, setIdle] = useState(false)
  const close = () => lightsOpen.set(false)
  useWakeLock()

  // The controls hide after three still seconds; a tap brings them back.
  const timer = useRef(0)
  const wake = () => {
    setIdle(false)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setIdle(true), 3000)
  }
  useEffect(() => {
    timer.current = window.setTimeout(() => setIdle(true), 3000)
    return () => window.clearTimeout(timer.current)
  }, [])

  useEffect(() => {
    void ref.current?.requestFullscreen?.().catch(() => {})
    const onFullscreen = () => {
      if (!document.fullscreenElement) close()
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
    }
    document.addEventListener('fullscreenchange', onFullscreen)
    window.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('fullscreenchange', onFullscreen)
      window.removeEventListener('keydown', onKey)
      if (document.fullscreenElement) void document.exitFullscreen().catch(() => {})
    }
  }, [])

  // Each frame sets the wash's color and brightness directly: no renders.
  useEffect(() => {
    const el = wash.current
    if (!el) return
    const colors = () => {
      const c = readColors(document.documentElement)
      return [c.vibrant, c.light, c.dominant, c.muted]
    }
    if (still) {
      el.style.backgroundColor = colors()[0]
      el.style.opacity = '0.35'
      return
    }
    let shown = -1
    let lastBar = NaN
    return onBeatFrame((f) => {
      // Bars counted from the grid, not from when this phone joined, so
      // every phone in the room is on the same color.
      const barIndex = Math.floor((f.beatIndex - f.bar) / 4)
      if (barIndex !== lastBar) {
        lastBar = barIndex
        const list = colors()
        const i = ((barIndex % list.length) + list.length) % list.length
        el.style.backgroundColor = list[i]
      }
      const flash = Math.min(1, f.downbeat * 1.3)
      const v = Math.min(1, IDLE * f.presence + flash * 0.88 + f.beat * 0.2)
      const rounded = Math.round(v * 100)
      if (rounded !== shown) {
        shown = rounded
        el.style.opacity = String(rounded / 100)
      }
    })
  }, [still, np?.artworkUrl])

  return (
    <motion.div
      ref={ref}
      role="dialog"
      aria-label="Lights"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.4 }}
      onPointerDown={wake}
      onPointerMove={wake}
      className={cn('fixed inset-0 z-[70] bg-black text-white', idle && 'cursor-none')}
    >
      <div ref={wash} aria-hidden className="absolute inset-0" style={{ opacity: IDLE }} />

      <div className={cn('absolute inset-0 transition-opacity duration-700', idle ? 'opacity-0' : 'opacity-100')}>
        <div className="absolute top-[calc(env(safe-area-inset-top)+1rem)] right-4">
          <Button size="icon" variant="glass" aria-label="Turn the lights off" onClick={close}>
            <X />
          </Button>
        </div>
        <div className="absolute inset-x-6 bottom-[calc(env(safe-area-inset-bottom)+2rem)] text-center">
          <p className="text-lg font-semibold">Lights</p>
          <p className="mx-auto max-w-xs text-sm text-white/70">
            {still
              ? level === 'off'
                ? 'Visuals are off, so the light holds steady.'
                : 'Reduce motion is on, so the light holds steady.'
              : np
                ? 'Hold your phone up. Every phone in lights mode flashes on the same beat.'
                : 'Nothing is playing yet.'}
          </p>
        </div>
      </div>
    </motion.div>
  )
}
