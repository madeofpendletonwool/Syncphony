import { describe, expect, it } from 'vitest'
import { hides, isOpen, plays, remaining, secondsUntil, type GameRound } from './games'

const at = (s: number) => new Date(Date.UTC(2026, 9, 8, 20, 0, s)).toISOString()
const ms = (s: number) => Date.parse(at(s))

const round = (over: Partial<GameRound> = {}): GameRound => ({
  id: 'r1',
  roomId: 'room',
  itemId: 'song',
  kind: 'year',
  mode: 'round',
  state: 'open',
  prompt: 'What year?',
  answer: 'number',
  choices: [],
  opensAt: at(10),
  closesAt: at(30),
  doneAt: at(40),
  answered: [],
  hides: ['notes'],
  guests: true,
  tvOnly: false,
  scores: 'board',
  difficulty: 0.5,
  ...over,
})

describe('games', () => {
  it('knows which levels run rounds', () => {
    expect(plays('off')).toBe(false)
    expect(plays('recap')).toBe(false)
    expect(plays('ambient')).toBe(true)
    expect(plays('gamenight')).toBe(true)
  })

  it('hides only what the round hides, only about its song, only until the reveal', () => {
    expect(hides(round(), 'song', 'notes')).toBe(true)
    expect(hides(round(), 'song', 'lyrics')).toBe(false)
    expect(hides(round(), 'other', 'notes')).toBe(false)
    expect(hides(round({ state: 'announce' }), 'song', 'notes')).toBe(true)
    expect(hides(round({ state: 'reveal' }), 'song', 'notes')).toBe(false)
    expect(hides(null, 'song', 'notes')).toBe(false)
  })

  it('times the answer window by the server clock', () => {
    expect(isOpen(round(), ms(20))).toBe(true)
    expect(isOpen(round(), ms(30))).toBe(false)
    expect(isOpen(round({ state: 'announce' }), ms(5))).toBe(false)
    expect(secondsUntil(at(30), ms(20) + 100)).toBe(10)
    expect(secondsUntil(at(30), ms(35))).toBe(0)
    expect(remaining(round(), ms(20))).toBeCloseTo(0.5)
    expect(remaining(round({ state: 'announce' }), ms(5))).toBe(1)
    expect(remaining(round({ state: 'reveal' }), ms(31))).toBe(0)
  })
})
