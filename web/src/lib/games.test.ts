import { describe, expect, it } from 'vitest'
import { HIDDEN_LINE, hiddenLineAt, hides, isOpen, lineParts, maskLine, plays, remaining, roundArtworkUrl, secondsUntil, splitPrompt, streakOf, type GameRound } from './games'

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
  stopsMusic: false,
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

  it('keeps back a lyric line, every time it comes back', () => {
    const lines = [
      { atMs: 0, text: 'We can be heroes' },
      { atMs: 4000, text: 'Just for one day' },
      { atMs: 8000, text: 'We can be heroes ' },
    ]
    const r = round({ kind: 'lyrics', hides: ['line'], atMs: 8000 })
    expect(hiddenLineAt(r, 'song')).toBe(8000)
    expect(hiddenLineAt(round({ ...r, state: 'reveal' }), 'song')).toBeUndefined()
    expect(hiddenLineAt(r, 'other')).toBeUndefined()
    expect(maskLine(lines, 8000).map((l) => l.text)).toEqual([HIDDEN_LINE, 'Just for one day', HIDDEN_LINE])
    expect(maskLine(lines, undefined)).toBe(lines)
  })

  it('splits a prompt into its question, and its line into words and blanks', () => {
    expect(splitPrompt('Fill in the blanks\n“I ____ you ____”')).toEqual({ question: 'Fill in the blanks', line: '“I ____ you ____”' })
    expect(splitPrompt('What year is this from?').line).toBeUndefined()
    expect(lineParts('“I ____ you, ____”')).toEqual([
      { text: '“I ', blank: false },
      { text: '____', blank: true },
      { text: ' you, ', blank: false },
      { text: '____', blank: true },
      { text: '”', blank: false },
    ])
  })

  it('finds the other song’s cover and your streak', () => {
    expect(roundArtworkUrl(round())).toBeUndefined()
    expect(roundArtworkUrl(round({ other: { title: 'Thank You', hasArtwork: true } }), 200)).toBe('/api/rooms/room/games/rounds/r1/artwork?size=200')
    const scores = { roomId: 'room', mode: 'board' as const, players: [{ userId: 'me', points: 1, correct: 1, answered: 1, streak: 3, bestStreak: 4 }] }
    expect(streakOf(scores, 'me')).toEqual({ streak: 3, best: 4 })
    expect(streakOf(scores, 'you')).toEqual({ streak: 0, best: 0 })
  })
})
