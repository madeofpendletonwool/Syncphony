import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { applyAccent, extractAccent, type Accent } from './accent'
import { DEFAULT_ACCENT, ensureContrast, textOn, toCss, toGamut, type Oklch } from './color'

// The album-art palette (MAD-714). The server works it out once per song
// and sends it with the queue item, so every phone in the room matches.
// This turns it into CSS variables, with text colors that pass WCAG AA.

export type Palette = components['schemas']['Palette']

/** The palette's color roles, as the CSS variables name them. */
export const ROLES = ['dominant', 'vibrant', 'muted', 'dark', 'light'] as const
type Role = (typeof ROLES)[number]

/**
 * A palette made from an accent alone, for art the server can't read (an
 * SVG) or no art at all. Grayscale art (null) gets neutral colors on the
 * default hue.
 */
export function fallbackPalette(accent: Accent | null): Palette {
  const h = accent?.h ?? DEFAULT_ACCENT.h
  const c = accent?.c ?? 0.02
  return {
    accent,
    dominant: { l: 0.45, c: c * 0.7, h },
    vibrant: { l: 0.65, c, h },
    muted: { l: 0.55, c: Math.min(c, 0.06), h },
    dark: { l: 0.22, c: Math.min(c, 0.05), h },
    light: { l: 0.92, c: Math.min(c, 0.04), h },
  }
}

/** The page backgrounds of each theme, as index.css sets --background. */
export function themeBackground(theme: 'dark' | 'light', h: number): Oklch {
  return theme === 'dark' ? { l: 0.13, c: 0.018, h } : { l: 0.975, c: 0.008, h }
}

/**
 * The CSS variables for a palette:
 *
 * - `--pal-<role>`: each color, for backgrounds and gradients.
 * - `--pal-on-<role>`: text over that color, at AA or better.
 * - `--pal-surface-{dark,light}` and `--pal-on-surface-{dark,light}`: a
 *   panel tinted by the art, and its text, per theme.
 * - `--pal-text-{dark,light}`: the vibrant color, adjusted to read as text
 *   on each theme's background (the current lyric line, say).
 *
 * index.css maps the per-theme ones to `--pal-surface`, `--pal-on-surface`
 * and `--pal-text` for whichever theme is on.
 */
export function paletteVars(p: Palette): Record<string, string> {
  const vars: Record<string, string> = {}
  for (const role of ROLES) {
    const color = toGamut(p[role as Role])
    vars[`--pal-${role}`] = toCss(color)
    vars[`--pal-on-${role}`] = toCss(textOn(color))
  }
  const h = p.accent?.h ?? DEFAULT_ACCENT.h
  const surfaces = {
    dark: toGamut({ l: Math.min(p.dark.l, 0.25), c: Math.min(p.dark.c, 0.06), h: p.dark.h }),
    light: toGamut({ l: Math.max(p.light.l, 0.94), c: Math.min(p.light.c, 0.04), h: p.light.h }),
  }
  for (const theme of ['dark', 'light'] as const) {
    vars[`--pal-surface-${theme}`] = toCss(surfaces[theme])
    vars[`--pal-on-surface-${theme}`] = toCss(textOn(surfaces[theme]))
    vars[`--pal-text-${theme}`] = toCss(ensureContrast(p.vibrant, themeBackground(theme, h)))
  }
  return vars
}

/** Tints the app with a palette: its accent, and the --pal-* variables. */
export function applyPalette(p: Palette, root: HTMLElement = document.documentElement) {
  applyAccent(p.accent ?? null, root)
  for (const [name, value] of Object.entries(paletteVars(p))) root.style.setProperty(name, value)
}

const loaded = new Map<string, Promise<Palette>>()

/**
 * A queued song's palette: the server's, or, when it can't read the art,
 * one made from the accent the browser finds in the image. Cached per item.
 */
export function loadPalette(roomId: string | undefined, itemId: string | undefined, artworkUrl: string): Promise<Palette> {
  const key = `${roomId}/${itemId}/${artworkUrl}`
  let hit = loaded.get(key)
  if (!hit) {
    const fromImage = () => extractAccent(artworkUrl).then(fallbackPalette)
    hit =
      roomId && itemId
        ? unwrap(api.GET('/rooms/{roomId}/queue/{itemId}/palette', { params: { path: { roomId, itemId } } })).catch(fromImage)
        : fromImage()
    loaded.set(key, hit)
  }
  return hit
}
