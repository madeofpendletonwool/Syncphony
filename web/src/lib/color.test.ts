import { describe, expect, it } from 'vitest'
import { accentChroma, pickAccent, rgbToOklch, unwrapHue } from './color'

function pixels(...runs: [count: number, rgb: [number, number, number]][]) {
  const out: number[] = []
  for (const [count, [r, g, b]] of runs) for (let i = 0; i < count; i++) out.push(r, g, b, 255)
  return new Uint8ClampedArray(out)
}

describe('rgbToOklch', () => {
  it('matches reference values', () => {
    const red = rgbToOklch(255, 0, 0)
    expect(red.l).toBeCloseTo(0.628, 3)
    expect(red.c).toBeCloseTo(0.2577, 3)
    expect(red.h).toBeCloseTo(29.23, 1)

    const blue = rgbToOklch(0, 0, 255)
    expect(blue.l).toBeCloseTo(0.452, 3)
    expect(blue.h).toBeCloseTo(264.05, 1)
  })

  it('gives grays zero chroma', () => {
    for (const v of [0, 128, 255]) {
      const g = rgbToOklch(v, v, v)
      expect(g.c).toBeLessThan(1e-3)
    }
    expect(rgbToOklch(255, 255, 255).l).toBeCloseTo(1, 3)
  })
})

describe('pickAccent', () => {
  it('finds a vivid color over a large gray background', () => {
    const accent = pickAccent(pixels([900, [90, 90, 90]], [100, [30, 120, 240]]))
    expect(accent).not.toBeNull()
    expect(accent!.h).toBeGreaterThan(240)
    expect(accent!.h).toBeLessThan(270)
  })

  it('prefers the more colorful of two hues', () => {
    // A dusty peach (~40°) and a hot pink (~2°), in equal amounts.
    const accent = pickAccent(pixels([300, [200, 150, 140]], [300, [230, 40, 120]]))
    expect(accent!.c).toBeGreaterThan(0.15)
    expect(Math.abs(accent!.h - rgbToOklch(230, 40, 120).h)).toBeLessThan(3)
  })

  it('handles hues that wrap past 360°', () => {
    // Pinks just either side of 0°.
    const accent = pickAccent(pixels([50, [235, 60, 120]], [50, [240, 70, 90]]))
    expect(accent).not.toBeNull()
    expect(Math.min(accent!.h, 360 - accent!.h)).toBeLessThan(25)
  })

  it('returns null for grayscale or transparent art', () => {
    expect(pickAccent(pixels([500, [20, 20, 20]], [500, [240, 240, 240]]))).toBeNull()
    expect(pickAccent(new Uint8ClampedArray([255, 0, 0, 0]))).toBeNull()
    expect(pickAccent(new Uint8ClampedArray())).toBeNull()
  })

  it('ignores near-black and near-white even when tinted', () => {
    expect(pickAccent(pixels([1000, [10, 0, 30]]))).toBeNull()
  })
})

describe('accentChroma', () => {
  it('clamps into a usable range', () => {
    expect(accentChroma(0.01)).toBe(0.07)
    expect(accentChroma(0.12)).toBe(0.12)
    expect(accentChroma(0.3)).toBe(0.19)
  })
})

describe('unwrapHue', () => {
  it('takes the short way around the wheel', () => {
    expect(unwrapHue(350, 10)).toBe(370)
    expect(unwrapHue(10, 350)).toBe(-10)
    expect(unwrapHue(370, 20)).toBe(380)
    expect(unwrapHue(100, 200)).toBe(200)
    expect(unwrapHue(720, 90)).toBe(810)
  })
})
