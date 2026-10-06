import { describe, expect, it } from 'vitest'
import { AA, contrast, ensureContrast, luminance, rgbToOklch, textOn, toGamut, type Oklch } from './color'
import { fallbackPalette, paletteVars, ROLES, themeBackground, type Palette } from './palette'

/** Parses toCss output back to a color. */
function parse(css: string): Oklch {
  const m = /^oklch\(([\d.]+) ([\d.]+) ([\d.-]+)\)$/.exec(css)
  if (!m) throw new Error(`not oklch: ${css}`)
  return { l: Number(m[1]), c: Number(m[2]), h: Number(m[3]) }
}

// A palette shaped like the server's, from dark, saturated art.
const synthwave: Palette = {
  accent: { h: 340, c: 0.19 },
  dominant: { l: 0.25, c: 0.09, h: 290 },
  vibrant: { l: 0.62, c: 0.24, h: 345 },
  muted: { l: 0.5, c: 0.07, h: 300 },
  dark: { l: 0.14, c: 0.05, h: 285 },
  light: { l: 0.88, c: 0.05, h: 330 },
}

describe('contrast', () => {
  it('matches WCAG', () => {
    const white = { l: 1, c: 0, h: 0 }
    const black = { l: 0, c: 0, h: 0 }
    expect(contrast(white, black)).toBeCloseTo(21, 1)
    expect(contrast(white, white)).toBeCloseTo(1, 5)
    // #767676 on white is the classic just-passes-AA gray (4.54:1).
    const gray = rgbToOklch(0x76, 0x76, 0x76)
    expect(contrast(gray, white)).toBeCloseTo(4.54, 1)
    expect(luminance({ l: 0.628, c: 0.2577, h: 29.23 })).toBeCloseTo(0.2126, 2) // sRGB red
  })
})

describe('toGamut', () => {
  it('lowers chroma until the color fits sRGB', () => {
    const wild = toGamut({ l: 0.9, c: 0.35, h: 140 })
    expect(wild.l).toBe(0.9)
    expect(wild.h).toBe(140)
    expect(wild.c).toBeLessThan(0.35)
    expect(wild.c).toBeGreaterThan(0.1)
    const fine = { l: 0.5, c: 0.05, h: 200 }
    expect(toGamut(fine)).toEqual(fine)
  })
})

describe('ensureContrast', () => {
  it('leaves colors that already pass alone', () => {
    const bg = { l: 0.13, c: 0.018, h: 295 }
    expect(ensureContrast({ l: 0.9, c: 0.05, h: 30 }, bg)).toEqual({ l: 0.9, c: 0.05, h: 30 })
  })

  it('moves lightness as little as it takes', () => {
    for (const bg of [themeBackground('dark', 120), themeBackground('light', 120), { l: 0.6, c: 0.1, h: 20 }]) {
      for (const fg of [
        { l: 0.5, c: 0.2, h: 30 },
        { l: 0.3, c: 0.1, h: 260 },
        { l: 0.95, c: 0.03, h: 90 },
      ]) {
        const out = ensureContrast(fg, bg)
        expect(contrast(out, bg)).toBeGreaterThanOrEqual(AA)
        expect(out.h).toBe(fg.h)
        // A hair less change would fail.
        const step = out.l > fg.l ? -0.01 : 0.01
        if (out.l !== fg.l) expect(contrast(toGamut({ ...out, l: out.l + step }), bg)).toBeLessThan(AA)
      }
    }
  })
})

describe('textOn', () => {
  it('picks light text on dark colors and dark text on light ones', () => {
    expect(textOn({ l: 0.2, c: 0.05, h: 280 }).l).toBeGreaterThan(0.9)
    expect(textOn({ l: 0.9, c: 0.05, h: 280 }).l).toBeLessThan(0.3)
  })
})

describe('paletteVars', () => {
  for (const [name, p] of [
    ['synthwave', synthwave],
    ['grayscale fallback', fallbackPalette(null)],
    ['vivid fallback', fallbackPalette({ h: 95, c: 0.19 })],
  ] as const) {
    it(`passes AA everywhere for ${name}`, () => {
      const v = paletteVars(p)
      for (const role of ROLES) {
        expect(contrast(parse(v[`--pal-on-${role}`]), parse(v[`--pal-${role}`]))).toBeGreaterThanOrEqual(AA)
      }
      const h = p.accent?.h ?? 295
      for (const theme of ['dark', 'light'] as const) {
        expect(contrast(parse(v[`--pal-on-surface-${theme}`]), parse(v[`--pal-surface-${theme}`]))).toBeGreaterThanOrEqual(AA)
        expect(contrast(parse(v[`--pal-text-${theme}`]), themeBackground(theme, h))).toBeGreaterThanOrEqual(AA)
      }
    })
  }

  it('keeps surfaces dark in the dark theme and light in the light one', () => {
    const v = paletteVars(synthwave)
    expect(parse(v['--pal-surface-dark']).l).toBeLessThanOrEqual(0.25)
    expect(parse(v['--pal-surface-light']).l).toBeGreaterThanOrEqual(0.94)
  })
})
