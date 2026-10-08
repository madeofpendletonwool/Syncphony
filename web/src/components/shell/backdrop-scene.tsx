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

/**
 * The scene layer. One canvas for good: a new song's scene crossfades in
 * on it, rather than on a second full-screen canvas (a pile of big
 * blurred layers is what makes a browser flash black).
 */
export function BackdropScene() {
  const scene = useScene()
  return <SceneCanvas paint={painters[scene]} />
}

/** How long one painter takes to fade into the next. */
const CROSSFADE_MS = 1600

/**
 * A canvas a painter draws on every frame the beat engine runs. Drawn at a
 * fraction of the element's size (`scale`) and blurred by its class, so it
 * reads as glow. A new painter crossfades in on the same canvas. In the
 * app it follows the beat lab's scene strength.
 */
export function SceneCanvas({ paint, scale = 1 / 2, app = true, className = 'opacity-70 blur-md dark:opacity-90' }: {
  paint: () => Painter
  scale?: number
  /** In the app's backdrop, rather than the big visualizer. */
  app?: boolean
  className?: string
}) {
  const ref = useRef<HTMLCanvasElement>(null)
  // The painter drawing now, the one fading out, and since when.
  const layers = useRef<{ make: () => Painter; draw: Painter; old?: Painter; since: number } | null>(null)

  useEffect(() => {
    const l = layers.current
    if (!l) layers.current = { make: paint, draw: paint(), since: -Infinity }
    else if (l.make !== paint) layers.current = { make: paint, draw: paint(), old: l.draw, since: performance.now() }
  }, [paint])

  useEffect(() => {
    const canvas = ref.current
    const ctx = canvas?.getContext('2d')
    if (!canvas || !ctx) return

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
      const l = layers.current
      if (f.now - readAt > 300) {
        colors = readColors(canvas)
        readAt = f.now
      }
      ctx.clearRect(0, 0, canvas.width, canvas.height)
      if (!l || f.presence < 0.002) return
      const strength = app ? beatSettings.get().effects.scene : 1
      if (strength === 0) return
      const t = Math.min(1, (f.now - l.since) / CROSSFADE_MS)
      if (t >= 1) l.old = undefined
      const layer = (draw: Painter, weight: number) => {
        // Painters fade with presence, so scaling it fades the whole layer.
        const presence = Math.min(1, f.presence * strength * weight)
        ctx.save()
        ctx.globalAlpha = presence
        draw(ctx, canvas.width, canvas.height, { ...f, presence }, colors)
        ctx.restore()
      }
      if (l.old) layer(l.old, 1 - t)
      layer(l.draw, t)
    })
    return () => {
      off()
      ro.disconnect()
    }
  }, [scale, app])

  return <canvas ref={ref} aria-hidden className={cn('absolute inset-0 size-full', className)} />
}
