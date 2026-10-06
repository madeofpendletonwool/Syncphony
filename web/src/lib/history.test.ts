import { describe, expect, it } from 'vitest'
import { formatListening, sessionName, sessionRange } from './history'

describe('formatListening', () => {
  it('reads naturally', () => {
    expect(formatListening(45 * 60_000)).toBe('45 min')
    expect(formatListening(120 * 60_000)).toBe('2 h')
    expect(formatListening(135 * 60_000)).toBe('2 h 15 min')
  })
})

describe('sessions', () => {
  it('names a session by its day and time of day', () => {
    const d = new Date(2026, 9, 2, 22, 30) // a Friday, local time
    expect(sessionName({ startedAt: d.toISOString() })).toMatch(/ night$/)
  })
  it('covers the whole session', () => {
    const r = sessionRange({ startedAt: '2026-10-02T20:00:00Z', endedAt: '2026-10-02T23:00:00Z', plays: 3, people: [] })
    expect(r.from).toBe('2026-10-02T20:00:00Z')
    expect(Date.parse(r.to!)).toBeGreaterThan(Date.parse('2026-10-02T23:00:00Z'))
  })
})
