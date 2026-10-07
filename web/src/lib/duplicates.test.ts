import { describe, expect, it } from 'vitest'
import { duplicateMessage, whereIs, type QueueDuplicate } from './duplicates'

const now = Date.parse('2026-10-07T20:00:00Z')
const names: Record<string, string> = { sam: 'Sam' }
const nameOf = (id: string) => names[id]
const dup = (item: Partial<QueueDuplicate['item']>, playedAt?: string) =>
  ({ title: 'Heroes', item: { state: 'queued', addedBy: 'sam', ...item }, playedAt }) as QueueDuplicate

describe('whereIs', () => {
  it('says whose lane it waits in', () => {
    expect(whereIs(dup({}), 'me', nameOf, now)).toBe('already in Sam’s lane')
    expect(whereIs(dup({ addedBy: 'me' }), 'me', nameOf, now)).toBe('already in your lane')
    expect(whereIs(dup({ addedBy: 'gone' }), 'me', nameOf, now)).toBe('already in someone’s lane')
  })
  it('knows playing, autopilot and played songs', () => {
    expect(whereIs(dup({ state: 'playing' }), 'me', nameOf, now)).toBe('playing now')
    expect(whereIs(dup({ autopilot: {} as never }), 'me', nameOf, now)).toBe('already up next')
    expect(whereIs(dup({ state: 'played' }, '2026-10-07T19:40:00Z'), 'me', nameOf, now)).toBe('played 20 minutes ago')
  })
})

describe('duplicateMessage', () => {
  it('names one song', () => {
    expect(duplicateMessage([dup({})], 'me', nameOf, now)).toBe('“Heroes” is already in Sam’s lane')
    expect(duplicateMessage([dup({ state: 'played' }, '2026-10-07T19:40:00Z')], 'me', nameOf, now)).toBe('“Heroes” played 20 minutes ago')
  })
  it('counts several', () => {
    expect(duplicateMessage([dup({}), dup({})], 'me', nameOf, now)).toBe('2 of these songs are already queued or played recently')
  })
})
