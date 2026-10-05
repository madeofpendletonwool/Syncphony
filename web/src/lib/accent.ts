import { accentChroma, DEFAULT_ACCENT, pickAccent, unwrapHue } from './color'

export type Accent = { h: number; c: number }

const SAMPLE = 32 // px; plenty for a dominant color and cheap on phones
const cache = new Map<string, Promise<Accent | null>>()

/**
 * Loads artwork and extracts its accent. Results are cached per URL.
 * Resolves null for grayscale art or images the canvas can't read (a
 * cross-origin image without CORS headers taints it).
 */
export function extractAccent(url: string): Promise<Accent | null> {
  let hit = cache.get(url)
  if (!hit) {
    hit = load(url).catch(() => null)
    cache.set(url, hit)
  }
  return hit
}

async function load(url: string): Promise<Accent | null> {
  const img = new Image()
  img.crossOrigin = 'anonymous'
  img.decoding = 'async'
  img.src = url
  await img.decode()

  const canvas = document.createElement('canvas')
  canvas.width = SAMPLE
  canvas.height = SAMPLE
  const ctx = canvas.getContext('2d', { willReadFrequently: true })
  if (!ctx) return null
  ctx.drawImage(img, 0, 0, SAMPLE, SAMPLE)
  const picked = pickAccent(ctx.getImageData(0, 0, SAMPLE, SAMPLE).data)
  return picked && { h: picked.h, c: accentChroma(picked.c) }
}

let currentHue: number = DEFAULT_ACCENT.h

/**
 * Sets the app-wide accent. The CSS transitions --album-h/--album-c, so the
 * hue is unwrapped to take the short way around the color wheel.
 */
export function applyAccent(accent: Accent | null, root: HTMLElement = document.documentElement) {
  const next = accent ?? DEFAULT_ACCENT
  currentHue = unwrapHue(currentHue, next.h)
  root.style.setProperty('--album-h', currentHue.toFixed(2))
  root.style.setProperty('--album-c', next.c.toFixed(4))
}
