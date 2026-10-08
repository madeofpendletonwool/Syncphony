import { describe, expect, it } from 'vitest'
import { beatAt, decode, loudnessAt, sectionEnergy, spectrumAt } from './beat-map'

const wire = {
  durationMs: 10_000,
  bpm: 120,
  confidence: 0.8,
  // A beat every 500 ms from 1 s; the first bar starts on the second beat.
  beats: Array.from({ length: 16 }, (_, i) => 1000 + i * 500),
  downbeat: 1,
  sections: [
    { startMs: 0, energy: 0.2 },
    { startMs: 5000, energy: 0.9 },
  ],
  frameRate: 2,
  bandCount: 2,
  // Two frames a second, two bands: [0, 255], [255, 0], ...
  bands: btoa(String.fromCharCode(0, 255, 255, 0, 0, 255)),
  loudness: btoa(String.fromCharCode(0, 128, 255)),
  features: { energy: 0.5, brightness: 0.5, dynamics: 0.5 },
}

describe('beat maps', () => {
  const m = decode(wire)

  it('decodes the envelopes', () => {
    expect([...m.bands]).toEqual([0, 255, 255, 0, 0, 255])
    expect([...m.loudness]).toEqual([0, 128, 255])
  })

  it('finds the beat and its place in the bar', () => {
    expect(beatAt(m, 999)).toBeNull()
    expect(beatAt(m, 1000)).toEqual({ index: 0, into: 0, period: 500, bar: 3 })
    expect(beatAt(m, 1600)).toEqual({ index: 1, into: 100, period: 500, bar: 0 })
    expect(beatAt(m, 3500)?.bar).toBe(0)
    // The last beat lasts as long as the one before it.
    expect(beatAt(m, 8700)).toEqual({ index: 15, into: 200, period: 500, bar: 2 })
    expect(beatAt(m, 9000)).toBeNull()
  })

  it('reads sections, loudness and the spectrum', () => {
    expect(sectionEnergy(m, 4999)).toBe(0.2)
    expect(sectionEnergy(m, 5000)).toBe(0.9)
    expect(loudnessAt(m, 600)).toBeCloseTo(128 / 255)
    expect(loudnessAt(m, 99_000)).toBe(0)
    const out = new Float32Array(2)
    // Halfway between the first two frames.
    expect([...spectrumAt(m, 250, out)]).toEqual([0.5, 0.5])
    expect([...spectrumAt(m, -10, out)]).toEqual([0, 0])
  })
})
