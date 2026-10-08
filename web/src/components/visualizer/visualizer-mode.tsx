import { ChevronLeft, ChevronRight, Pause, Play, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Artwork } from '@/components/artwork'
import { Button } from '@/components/ui/button'
import { usePosition } from '@/hooks/use-position'
import { SHOWS, useShow } from '@/hooks/use-scene'
import { beatSettings, setBeatSettings, visualizerOpen } from '@/lib/beat'
import { formatDuration, usePlayer } from '@/lib/now-playing'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'
import { Visualizer } from './visualizer'

/**
 * The app's full-window visualizer (MAD-780): the song's visuals filling
 * the screen (full screen on a computer), with what's playing small in a
 * corner. The controls fade away while the mouse rests. ← and → change
 * the show, space plays and pauses, Esc closes.
 */
export function VisualizerMode() {
  const open = useStore(visualizerOpen)
  return createPortal(<AnimatePresence>{open && <Mode />}</AnimatePresence>, document.body)
}

function Mode() {
  const { nowPlaying: np, commands } = usePlayer()
  const position = usePosition(np)
  const { show: setting } = useStore(beatSettings)
  const show = useShow(setting)
  const ref = useRef<HTMLDivElement>(null)
  const [idle, setIdle] = useState(false)
  const close = () => visualizerOpen.set(false)

  // Cycles through the shows; past the last comes back to auto.
  const step = (by: number) => {
    const list = ['auto', ...SHOWS]
    const i = list.indexOf(setting)
    setBeatSettings({ show: list[(i + by + list.length) % list.length] })
  }

  // The controls hide after three still seconds.
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
    const el = ref.current
    // Full screen where the browser allows; a window otherwise.
    void el?.requestFullscreen?.().catch(() => {})
    return () => {
      if (document.fullscreenElement) void document.exitFullscreen().catch(() => {})
    }
  }, [])

  useEffect(() => {
    // Leaving full screen (Esc in most browsers) closes it too.
    const onFullscreen = () => {
      if (!document.fullscreenElement) close()
    }
    document.addEventListener('fullscreenchange', onFullscreen)
    return () => document.removeEventListener('fullscreenchange', onFullscreen)
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
      else if (e.key === 'ArrowRight') step(1)
      else if (e.key === 'ArrowLeft') step(-1)
      else if (e.key === ' ' && commands.toggle) {
        e.preventDefault()
        commands.toggle()
      } else return
      wake()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  const progress = np && np.track.durationMs > 0 ? position / np.track.durationMs : 0

  return (
    <motion.div
      ref={ref}
      role="dialog"
      aria-label="Visualizer"
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.5 }}
      onPointerMove={wake}
      onPointerDown={wake}
      className={cn('fixed inset-0 z-[70] bg-black text-white', idle && 'cursor-none')}
    >
      <Visualizer show={show} artworkUrl={np?.artworkUrl} />

      <div className={cn('absolute inset-0 transition-opacity duration-700', idle ? 'opacity-0' : 'opacity-100')}>
        <div className="absolute top-[calc(env(safe-area-inset-top)+1rem)] right-4 flex gap-2">
          <Button size="icon" variant="glass" aria-label="Close the visualizer" onClick={close}>
            <X />
          </Button>
        </div>

        <div className="absolute inset-x-4 bottom-[calc(env(safe-area-inset-bottom)+1rem)] flex items-end justify-between gap-4 sm:inset-x-8 sm:bottom-8">
          {np && (
            <div className="glass flex min-w-0 items-center gap-3 rounded-2xl p-2.5 pr-4 sm:gap-4">
              <Artwork src={np.artworkUrl} className="size-14 rounded-xl sm:size-16" />
              <div className="min-w-0">
                <p className="truncate font-semibold sm:text-lg">{np.track.title}</p>
                <p className="truncate text-sm text-white/70">{np.track.artists.join(', ')}</p>
                <div className="mt-1.5 flex items-center gap-2 text-xs text-white/60 tabular-nums">
                  <span>{formatDuration(position)}</span>
                  <span className="h-1 w-24 overflow-hidden rounded-full bg-white/20 sm:w-40">
                    <span className="block h-full origin-left bg-white/80" style={{ transform: `scaleX(${progress})` }} />
                  </span>
                  <span>{formatDuration(np.track.durationMs)}</span>
                </div>
              </div>
              {commands.toggle && (
                <Button size="icon" variant="ghost" aria-label={np.paused ? 'Play' : 'Pause'} onClick={commands.toggle} className="ml-1 shrink-0">
                  {np.paused ? <Play /> : <Pause />}
                </Button>
              )}
            </div>
          )}

          <div className="glass flex shrink-0 items-center gap-1 rounded-full p-1">
            <Button size="icon-sm" variant="ghost" aria-label="Previous show" onClick={() => step(-1)}>
              <ChevronLeft />
            </Button>
            <span className="min-w-24 text-center text-sm capitalize">{setting === 'auto' ? `Auto · ${show}` : show}</span>
            <Button size="icon-sm" variant="ghost" aria-label="Next show" onClick={() => step(1)}>
              <ChevronRight />
            </Button>
          </div>
        </div>
      </div>
    </motion.div>
  )
}
