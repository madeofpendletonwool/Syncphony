import { describe, expect, it } from 'vitest'
import { activeLine, getOffset, inGap, lineProgress, MAX_OFFSET_MS, setOffset } from './lyrics'

const lines = [
  { atMs: 1000, text: 'one' },
  { atMs: 3000, text: 'two' },
  { atMs: 5000, text: '' },
  { atMs: 30_000, text: 'three' },
]

describe('activeLine', () => {
  it('finds the line that has started', () => {
    expect(activeLine(lines, 0)).toBe(-1)
    expect(activeLine(lines, 1000)).toBe(0)
    expect(activeLine(lines, 2999)).toBe(0)
    expect(activeLine(lines, 3000)).toBe(1)
    expect(activeLine(lines, 99_000)).toBe(3)
    expect(activeLine([], 5)).toBe(-1)
  })
})

describe('lineProgress', () => {
  it('runs from 0 to 1 across a line', () => {
    expect(lineProgress(lines, 0, 2000, 60_000)).toBe(0.5)
    expect(lineProgress(lines, -1, 500, 60_000)).toBe(0)
    expect(lineProgress(lines, 3, 45_000, 60_000)).toBe(0.5)
  })
})

describe('inGap', () => {
  it('spots a long instrumental break', () => {
    expect(inGap(lines, 2000)).toBe(false)
    expect(inGap(lines, 5500)).toBe(false) // just started; let it settle
    expect(inGap(lines, 8000)).toBe(true)
    expect(inGap(lines, 26_000)).toBe(false) // words soon
  })
  it('counts a long intro', () => {
    const late = [{ atMs: 20_000, text: 'finally' }]
    expect(inGap(late, 5000)).toBe(true)
    expect(inGap(lines, 500)).toBe(false)
  })
})

describe('offsets', () => {
  it('are kept per song, clamped, and dropped at zero', () => {
    setOffset('fake:t1', 500)
    expect(getOffset('fake:t1')).toBe(500)
    expect(getOffset('fake:t2')).toBe(0)
    setOffset('fake:t1', 99_999)
    expect(getOffset('fake:t1')).toBe(MAX_OFFSET_MS)
    setOffset('fake:t1', 0)
    expect(getOffset('fake:t1')).toBe(0)
  })
})
