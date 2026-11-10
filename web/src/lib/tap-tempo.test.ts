import { describe, expect, it } from 'vitest'
import { beatAt, decode } from './beat-map'
import { alignToMap, fitTaps, mod4 } from './tap-tempo'

// 120 BPM, a beat every 500 ms from 1 s; the first bar starts on the second beat (1500).
const map = decode({
  durationMs: 60_000,
  bpm: 120,
  confidence: 0.8,
  beats: Array.from({ length: 110 }, (_, i) => 1000 + i * 500),
  downbeat: 1,
  sections: [{ startMs: 0, energy: 0.5 }],
  frameRate: 2,
  bandCount: 1,
  bands: btoa(String.fromCharCode(0)),
  loudness: btoa(String.fromCharCode(0)),
  features: { energy: 0.5, brightness: 0.5, dynamics: 0.5 },
})

// Wobbles a tap by up to ±15 ms, the way a thumb does.
const wobble = [7, -12, 3, 15, -6, -14, 9, 0, -3, 11]
const tapsAt = (first: number, period: number, n: number) => Array.from({ length: n }, (_, i) => first + i * period + wobble[i % 10])

describe('fitting taps', () => {
  it('needs four taps', () => {
    expect(fitTaps([0, 500, 1000])).toBeNull()
  })

  it('finds the tempo and the first beat', () => {
    const fit = fitTaps(tapsAt(2000, 600, 8))!
    expect(60000 / fit.period).toBeCloseTo(100, 0)
    expect(Math.abs(fit.firstMs - 2000)).toBeLessThan(15)
  })

  it('counts a skipped beat rather than stretching the tempo', () => {
    const t = tapsAt(0, 500, 8)
    t.splice(4, 1)
    const fit = fitTaps(t)!
    expect(60000 / fit.period).toBeCloseTo(120, 0)
  })

  it('ignores a double tap', () => {
    const t = tapsAt(0, 500, 8)
    t.splice(3, 0, t[2] + 60)
    const fit = fitTaps(t)!
    expect(60000 / fit.period).toBeCloseTo(120, 0)
    expect(Math.abs(fit.firstMs)).toBeLessThan(15)
  })
})

describe('aligning taps to the map', () => {
  // Where the shifted map puts beat and bar for a position.
  const placeAt = (pos: number, s: { offsetMs: number; barShift: number }) => {
    const p = beatAt(map, pos - s.offsetMs)!
    return { into: p.into, bar: mod4(p.bar - s.barShift) }
  }

  it('moves the beats to the taps, and makes the first tap the one', () => {
    // The real beat is 80 ms after the map's, and the user starts on map beat 4 (bar position 3).
    const taps = tapsAt(1000 + 4 * 500 + 80, 500, 8)
    const s = alignToMap(map, taps, fitTaps(taps)!.period)!
    expect(s.offsetMs).toBeGreaterThan(70)
    expect(s.offsetMs).toBeLessThan(90)
    const at = placeAt(1000 + 4 * 500 + 80 + 30, s)
    expect(Math.abs(at.into - 30)).toBeLessThan(15)
    expect(at.bar).toBe(0)
  })

  it('handles a map that sits on the off-beat', () => {
    const taps = tapsAt(1000 + 250, 500, 8)
    const s = alignToMap(map, taps, fitTaps(taps)!.period)!
    expect(Math.abs(Math.abs(s.offsetMs) - 250)).toBeLessThan(15)
    const at = placeAt(1250 + 30, s)
    expect(Math.abs(at.into - 30)).toBeLessThan(20)
    expect(at.bar).toBe(0)
  })

  it('accepts taps on every other beat', () => {
    const taps = tapsAt(3000 - 40, 1000, 6)
    const s = alignToMap(map, taps, fitTaps(taps)!.period)!
    expect(s.offsetMs).toBeCloseTo(-40, -1)
  })

  it('leaves a different tempo to a grid of its own', () => {
    const taps = tapsAt(3000, 430, 8)
    expect(alignToMap(map, taps, fitTaps(taps)!.period)).toBeNull()
  })
})
