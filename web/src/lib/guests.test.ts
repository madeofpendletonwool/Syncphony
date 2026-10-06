import { describe, expect, it } from 'vitest'
import { endOfNight } from './guests'

describe('endOfNight', () => {
  const at = (h: number, m = 0) => new Date(2026, 9, 3, h, m)

  it('ends at 4am the next morning', () => {
    expect(endOfNight(at(20))).toEqual(new Date(2026, 9, 4, 4))
  })

  it('ends at 4am today after midnight', () => {
    expect(endOfNight(at(0, 30))).toEqual(new Date(2026, 9, 3, 4))
  })

  it('gives at least two hours in the small hours', () => {
    expect(endOfNight(at(3))).toEqual(at(5))
  })

  it('ends tomorrow at 4am in the morning', () => {
    expect(endOfNight(at(9))).toEqual(new Date(2026, 9, 4, 4))
  })
})
