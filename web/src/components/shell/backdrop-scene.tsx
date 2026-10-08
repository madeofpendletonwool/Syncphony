import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useRef } from 'react'
import { useScene } from '@/hooks/use-scene'
import { beatSettings, onBeatFrame } from '@/lib/beat'
import { painters, readColors, type Painter } from '@/lib/scenes'
import { cn } from '@/lib/utils'

/*
 * Backdrop scenes (MAD-782): a canvas over the palette mesh that draws
 * something music-shaped in the art's colors (lib/scenes.ts). Each song
 * gets one; the pick is in use-scene.ts.
 *
 * Drawn at half the screen's resolution, then softened with a blur: glow,
 * not pixels, and a phone fills a quarter of the pixels a frame.
 */

/** The scene layer, crossfading from one song's scene to the next. */
export function BackdropScene() {
  const scene = useScene()
  return (
    <AnimatePresence>
      <motion.div
        key={scene}
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        transition={{ duration: 1.6 }}
        className="absolute inset-0"
      >
        <SceneCanvas paint={painters[scene]} />
      </motion.div>
    </AnimatePresence>
  )
}

/**
 * A canvas a painter draws on every frame the beat engine runs. Drawn at a
 * fraction of the element's size (`scale`) and blurred by its class, so it
 * reads as glow. In the app it follows the beat lab's scene strength.
 */
export function SceneCanvas({ paint, scale = 1 / 2, app = true, className = 'opacity-70 blur-md dark:opacity-90' }: {
  paint: () => Painter
  scale?: number
  /** In the app's backdrop, rather than the big visualizer. */
  app?: boolean
  className?: string
}) {
  const ref = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = ref.current
    const ctx = canvas?.getContext('2d')
    if (!canvas || !ctx) return
    const draw = paint()

    const fit = () => {
      canvas.width = Math.max(1, Math.round(canvas.clientWidth * scale))
      canvas.height = Math.max(1, Math.round(canvas.clientHeight * scale))
    }
    fit()
    const ro = new ResizeObserver(fit)
    ro.observe(canvas)

    // The palette eases between songs; follow it a few times a second.
    let colors = readColors(canvas)
    let readAt = 0

    const off = onBeatFrame((f) => {
      if (f.now - readAt > 300) {
        colors = readColors(canvas)
        readAt = f.now
      }
      ctx.clearRect(0, 0, canvas.width, canvas.height)
      if (f.presence < 0.002) return
      const strength = app ? beatSettings.get().effects.scene : 1
      if (strength === 0) return
      ctx.save()
      ctx.globalAlpha = Math.min(1, f.presence * strength)
      draw(ctx, canvas.width, canvas.height, f, colors)
      ctx.restore()
    })
    return () => {
      off()
      ro.disconnect()
    }
  }, [paint, scale, app])

  return <canvas ref={ref} aria-hidden className={cn('absolute inset-0 size-full', className)} />
}
