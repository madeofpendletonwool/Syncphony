import { describe, expect, it } from 'vitest'
import { initials, laneStyle } from './lane'

describe('initials', () => {
  it('uses the first and last word', () => {
    expect(initials('Ada Lovelace')).toBe('AL')
    expect(initials('  grace  brewster murray hopper ')).toBe('GH')
    expect(initials('Collin')).toBe('C')
    expect(initials('')).toBe('?')
  })

  it('keeps emoji whole', () => {
    expect(initials('🎧 Dj')).toBe('🎧D')
  })
})

describe('laneStyle', () => {
  it('exposes the color as --lane', () => {
    expect(laneStyle('#7c3aed')).toEqual({ '--lane': '#7c3aed' })
    expect(laneStyle(undefined)).toBeUndefined()
  })
})
