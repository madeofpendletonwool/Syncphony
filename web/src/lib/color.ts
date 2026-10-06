// Color math for the album-art accent. Pure functions, no DOM, so they're
// easy to test; src/lib/accent.ts does the image loading.

export type Oklch = { l: number; c: number; h: number }

/** The accent the app falls back to with no artwork: Syncphony violet. */
export const DEFAULT_ACCENT = { h: 295, c: 0.16 } as const

function toLinear(v: number) {
  const s = v / 255
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}

/** Converts 0-255 sRGB to OKLCH (L 0-1, C ~0-0.37, H degrees 0-360). */
export function rgbToOklch(r: number, g: number, b: number): Oklch {
  const lr = toLinear(r)
  const lg = toLinear(g)
  const lb = toLinear(b)

  const l_ = Math.cbrt(0.4122214708 * lr + 0.5363325363 * lg + 0.0514459929 * lb)
  const m_ = Math.cbrt(0.2119034982 * lr + 0.6806995451 * lg + 0.1073969566 * lb)
  const s_ = Math.cbrt(0.0883024619 * lr + 0.2817188376 * lg + 0.6299787005 * lb)

  const L = 0.2104542553 * l_ + 0.793617785 * m_ - 0.0040720468 * s_
  const A = 1.9779984951 * l_ - 2.428592205 * m_ + 0.4505937099 * s_
  const B = 0.0259040371 * l_ + 0.7827717662 * m_ - 0.808675766 * s_

  const c = Math.hypot(A, B)
  let h = (Math.atan2(B, A) * 180) / Math.PI
  if (h < 0) h += 360
  return { l: L, c, h: c < 1e-4 ? 0 : h }
}

const BUCKETS = 24 // 15° each
const MIN_CHROMA = 0.04 // below this a pixel is effectively gray
// Minimum winning score per pixel: roughly 2% of the image at moderate
// chroma. Below that the art is effectively grayscale.
const MIN_SCORE = 0.002

/**
 * Picks the dominant vivid hue from RGBA pixel data (e.g. a small canvas
 * draw of the artwork). Pixels are weighted by chroma and by how far their
 * lightness is from black or white, so a splash of saturated color beats a
 * large gray background. Returns null for grayscale art, so the caller can
 * keep a neutral accent.
 */
export function pickAccent(pixels: ArrayLike<number>): { h: number; c: number } | null {
  const weight = new Float64Array(BUCKETS)
  const sinSum = new Float64Array(BUCKETS)
  const cosSum = new Float64Array(BUCKETS)
  const chromaSum = new Float64Array(BUCKETS)
  let counted = 0

  for (let i = 0; i + 3 < pixels.length; i += 4) {
    if (pixels[i + 3] < 128) continue
    counted++
    const { l, c, h } = rgbToOklch(pixels[i], pixels[i + 1], pixels[i + 2])
    if (c < MIN_CHROMA) continue
    // Peaks at mid lightness; near-black and near-white count for little.
    const lightness = Math.max(0, 1 - Math.abs(l - 0.6) / 0.45)
    const w = c * lightness
    if (w <= 0) continue
    const bucket = Math.floor(h / (360 / BUCKETS)) % BUCKETS
    const rad = (h * Math.PI) / 180
    weight[bucket] += w
    sinSum[bucket] += Math.sin(rad) * w
    cosSum[bucket] += Math.cos(rad) * w
    chromaSum[bucket] += c * w
  }
  if (counted === 0) return null

  // Score each bucket together with its neighbors so a hue that straddles a
  // bucket edge isn't split in two.
  let best = -1
  let bestScore = 0
  for (let b = 0; b < BUCKETS; b++) {
    const score = weight[b] + 0.5 * (weight[(b + 1) % BUCKETS] + weight[(b + BUCKETS - 1) % BUCKETS])
    if (score > bestScore) {
      bestScore = score
      best = b
    }
  }
  if (best < 0 || bestScore / counted < MIN_SCORE) return null

  let sin = 0
  let cos = 0
  let w = 0
  let chroma = 0
  for (const b of [(best + BUCKETS - 1) % BUCKETS, best, (best + 1) % BUCKETS]) {
    sin += sinSum[b]
    cos += cosSum[b]
    w += weight[b]
    chroma += chromaSum[b]
  }
  let h = (Math.atan2(sin, cos) * 180) / Math.PI
  if (h < 0) h += 360
  return { h, c: chroma / w }
}

/**
 * Clamps an extracted chroma into a range that reads as an accent in both
 * themes: vivid art doesn't burn, muted art still has some color.
 */
export function accentChroma(c: number) {
  return Math.min(0.19, Math.max(0.07, c))
}

/**
 * Returns `next` shifted by whole turns to be within 180° of `prev`.
 * --album-h is animated as a plain number, so going from 350 to 10 would
 * otherwise sweep through every hue on the way.
 */
export function unwrapHue(prev: number, next: number) {
  const delta = ((((next - prev) % 360) + 540) % 360) - 180
  return prev + delta
}

// --- Contrast -------------------------------------------------------------------

/** Converts OKLCH to linear-light sRGB, which may be out of gamut (outside 0-1). */
export function oklchToLinearRgb({ l, c, h }: Oklch): [number, number, number] {
  const rad = (h * Math.PI) / 180
  const a = c * Math.cos(rad)
  const b = c * Math.sin(rad)
  const l_ = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m_ = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s_ = (l - 0.0894841775 * a - 1.291485548 * b) ** 3
  return [
    4.0767416621 * l_ - 3.3077115913 * m_ + 0.2309699292 * s_,
    -1.2684380046 * l_ + 2.6097574011 * m_ - 0.3413193965 * s_,
    -0.0041960863 * l_ - 0.7034186147 * m_ + 1.707614701 * s_,
  ]
}

const inGamut = (rgb: number[]) => rgb.every((v) => v >= -1e-4 && v <= 1 + 1e-4)

/**
 * Brings a color into sRGB by lowering its chroma, keeping lightness and
 * hue, so what we check for contrast is what the screen shows.
 */
export function toGamut(color: Oklch): Oklch {
  const l = Math.min(1, Math.max(0, color.l))
  if (inGamut(oklchToLinearRgb({ ...color, l }))) return { ...color, l }
  let lo = 0
  let hi = color.c
  for (let i = 0; i < 20; i++) {
    const mid = (lo + hi) / 2
    if (inGamut(oklchToLinearRgb({ l, c: mid, h: color.h }))) lo = mid
    else hi = mid
  }
  return { l, c: lo, h: color.h }
}

/** WCAG relative luminance, 0 (black) to 1 (white). */
export function luminance(color: Oklch) {
  const [r, g, b] = oklchToLinearRgb(toGamut(color)).map((v) => Math.min(1, Math.max(0, v)))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

/** WCAG contrast ratio between two colors, 1 to 21. */
export function contrast(a: Oklch, b: Oklch) {
  const la = luminance(a)
  const lb = luminance(b)
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)
}

/** WCAG AA for normal text. */
export const AA = 4.5

/**
 * Returns fg, lightened or darkened as little as it takes to reach ratio
 * against bg. It goes whichever way can get there, lighter first on dark
 * backgrounds. The result is in gamut.
 */
export function ensureContrast(fg: Oklch, bg: Oklch, ratio = AA): Oklch {
  const start = toGamut(fg)
  if (contrast(start, bg) >= ratio) return start
  const lighterFirst = luminance(bg) < 0.18
  for (const lighter of lighterFirst ? [true, false] : [false, true]) {
    const end = lighter ? 1 : 0
    if (contrast(toGamut({ ...start, l: end }), bg) < ratio) continue
    // Binary search for the lightness nearest fg's that's enough.
    let near = start.l
    let far = end
    for (let i = 0; i < 24; i++) {
      const mid = (near + far) / 2
      if (contrast(toGamut({ ...start, l: mid }), bg) >= ratio) far = mid
      else near = mid
    }
    // Round the way that keeps it passing once written as CSS (toCss).
    const l = lighter ? Math.ceil(far * 1000) / 1000 : Math.floor(far * 1000) / 1000
    return toGamut({ ...start, l: Math.min(1, Math.max(0, l)) })
  }
  // Neither way reaches it (a mid-gray background asking for 21:1): the best we can do.
  const black = { l: 0, c: 0, h: 0 }
  const white = { l: 1, c: 0, h: 0 }
  return contrast(black, bg) > contrast(white, bg) ? black : white
}

/** Text for on top of bg: a near-white or near-black tinted with bg's hue, at AA or better. */
export function textOn(bg: Oklch, ratio = AA): Oklch {
  const light = { l: 0.98, c: Math.min(bg.c, 0.015), h: bg.h }
  const dark = { l: 0.18, c: Math.min(bg.c, 0.03), h: bg.h }
  const pick = contrast(light, bg) >= contrast(dark, bg) ? light : dark
  return ensureContrast(pick, bg, ratio)
}

/** A color as CSS, `oklch(L C H)`. */
export function toCss({ l, c, h }: Oklch) {
  return `oklch(${l.toFixed(3)} ${c.toFixed(4)} ${h.toFixed(2)})`
}
