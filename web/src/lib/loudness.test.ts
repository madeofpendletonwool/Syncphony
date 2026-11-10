import { describe, expect, it } from 'vitest'
import { TARGET_LUFS, volumeFor } from './loudness'

const db = (v: number) => 20 * Math.log10(v)

describe('evening out the volume', () => {
  it('turns a loud master down to the target', () => {
    // A modern master at -8 LUFS comes down 8 dB.
    expect(db(volumeFor(-8))).toBeCloseTo(TARGET_LUFS + 8, 5)
  })

  it("leaves songs at or under the target alone (it can't turn up)", () => {
    expect(volumeFor(TARGET_LUFS)).toBe(1)
    expect(volumeFor(-21)).toBe(1)
  })

  it('cuts 12 dB at most', () => {
    expect(db(volumeFor(5))).toBeCloseTo(-12, 5)
  })

  it('plays at full volume when the loudness is unknown', () => {
    expect(volumeFor(undefined)).toBe(1)
    expect(volumeFor(null)).toBe(1)
    expect(volumeFor(Number.NaN)).toBe(1)
  })
})
