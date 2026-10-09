import { describe, expect, it } from 'vitest'
import { clipUrl, nextClip } from './game-clips'
import type { GameRound } from './games'

const at = (s: number) => new Date(Date.UTC(2026, 9, 8, 20, 0, s)).toISOString()
const ms = (s: number) => Date.parse(at(s))

const round = (clips: GameRound['clips']): GameRound => ({
  id: 'r1',
  roomId: 'room',
  itemId: 'song',
  kind: 'tune',
  mode: 'round',
  state: 'open',
  prompt: 'Name that tune',
  answer: 'choice',
  choices: ['A', 'B', 'C', 'D'],
  opensAt: at(10),
  closesAt: at(40),
  doneAt: at(50),
  answered: [],
  hides: [],
  guests: true,
  tvOnly: false,
  scores: 'board',
  difficulty: 0.5,
  stopsMusic: true,
  clips,
})

describe('game clips', () => {
  it('addresses a clip by its opaque ID only', () => {
    expect(clipUrl('room 1', 'c/1')).toBe('/api/rooms/room%201/games/clips/c%2F1')
  })

  it('plays the newest clip once, and never one long over', () => {
    const one = { id: 'one', lengthMs: 1000, at: at(10), reveal: false }
    const two = { id: 'two', lengthMs: 2000, at: at(16), reveal: false }
    expect(nextClip(round([]), new Set(), ms(10))).toBeUndefined()
    expect(nextClip(round([one]), new Set(), ms(10))?.id).toBe('one')
    expect(nextClip(round([one]), new Set(['one']), ms(10))).toBeUndefined()
    // A phone that missed the first goes straight to the second.
    expect(nextClip(round([one, two]), new Set(), ms(16))?.id).toBe('two')
    // Too late: it's over.
    expect(nextClip(round([one, two]), new Set(), ms(25))).toBeUndefined()
  })
})
