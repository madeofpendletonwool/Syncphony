import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useRef, useState } from 'react'
import { Artwork } from '@/components/artwork'
import { SceneCanvas } from '@/components/shell/backdrop-scene'
import type { Show } from '@/hooks/use-scene'
import { onBeatFrame } from '@/lib/beat'
import { painters, readColors, showPainters } from '@/lib/scenes'
import { cn } from '@/lib/utils'
import { Fluid, toRgb, type FluidColors } from './fluid'

/**
 * The big visualizer (MAD-779, MAD-780): full-screen visuals in the
 * album's colors, moving with the song's beat map. For the TV and the
 * app's full-window mode. Nothing here is subtle.
 */
export function Visualizer({ show, artworkUrl, className }: { show: Show; artworkUrl?: string; className?: string }) {
  return (
    <div
      aria-hidden
      className={cn(
        'pointer-events-none absolute inset-0 overflow-hidden bg-[radial-gradient(circle_at_center,var(--pal-dark),black)]',
        className,
      )}
    >
      <AnimatePresence>
        <motion.div
          key={show}
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 1.4 }}
          className="absolute inset-0"
        >
          <ShowLayer show={show} artworkUrl={artworkUrl} />
        </motion.div>
      </AnimatePresence>
    </div>
  )
}

// Sharp shows draw at the screen's resolution, up to 1.5 pixels a point.
const sharp = typeof devicePixelRatio === 'number' ? Math.min(devicePixelRatio, 1.5) : 1

function ShowLayer({ show, artworkUrl }: { show: Show; artworkUrl?: string }) {
  if (show === 'fluid') return <FluidCanvas />
  if (show === 'spectrum') {
    return (
      <>
        <SceneCanvas paint={showPainters.spectrum} scale={sharp} app={false} className="" />
        {/* The art sits inside the ring. */}
        <div className="absolute inset-0 flex items-center justify-center">
          <Artwork
            src={artworkUrl}
            className="size-[34vmin] rounded-full shadow-[0_0_80px_-10px_var(--glow)] outline-none"
          />
        </div>
      </>
    )
  }
  if (show === 'tunnel' || show === 'rain' || show === 'sparks') {
    return <SceneCanvas paint={showPainters[show]} scale={sharp} app={false} className="" />
  }
  // The backdrop's scenes, full strength.
  return <SceneCanvas paint={painters[show]} scale={1 / 2} app={false} className="blur-md" />
}

/** The palette fluid, or the aurora where there's no WebGL. */
function FluidCanvas() {
  const ref = useRef<HTMLDivElement>(null)
  const [webgl] = useState(hasWebGL)

  useEffect(() => {
    const box = ref.current
    if (!box || !webgl) return
    // A canvas of its own each time: a context let go can't be had back,
    // and React may mount this twice (strict mode).
    const canvas = document.createElement('canvas')
    canvas.className = 'absolute inset-0 size-full'
    box.append(canvas)
    let fluid: Fluid
    try {
      fluid = new Fluid(canvas)
    } catch (err) {
      console.warn('visualizer: no fluid', err)
      canvas.remove()
      return
    }
    let colors: FluidColors | null = null
    let readAt = -Infinity
    const off = onBeatFrame((f) => {
      if (f.now - readAt > 300) {
        const c = readColors(box)
        const dark = getComputedStyle(box).getPropertyValue('--pal-dark').trim() || 'black'
        colors = { dark: toRgb(dark), dominant: toRgb(c.dominant), vibrant: toRgb(c.vibrant), light: toRgb(c.light) }
        readAt = f.now
      }
      if (colors) fluid.render(f, colors)
    })
    return () => {
      off()
      fluid.dispose()
      canvas.remove()
    }
  }, [webgl])

  if (!webgl) return <SceneCanvas paint={painters.aurora} scale={1 / 2} app={false} className="blur-md" />
  return <div ref={ref} className="absolute inset-0" />
}

let webglChecked: boolean | undefined

/** Whether the device can draw WebGL; asked once, and the test context let go. */
function hasWebGL() {
  if (webglChecked === undefined) {
    try {
      const gl = document.createElement('canvas').getContext('webgl')
      webglChecked = !!gl
      gl?.getExtension('WEBGL_lose_context')?.loseContext()
    } catch {
      webglChecked = false
    }
  }
  return webglChecked
}
