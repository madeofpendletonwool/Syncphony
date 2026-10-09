import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import type { QueueItem } from './playback'
import {
  chainFor,
  champion,
  closeness,
  currentMatch,
  enterable,
  entriesLeft,
  lastMatch,
  mayClose,
  putQueueGame,
  queueGamesOn,
  queueGamesQuery,
  roundName,
  type GameEntry,
  type GameMatch,
  type QueueGame,
} from './queue-games'
import type { RoomGames } from './games'

const at = (s: number) => new Date(Date.UTC(2026, 9, 8, 20, 0, s)).toISOString()

const game = (over: Partial<QueueGame> = {}): QueueGame => ({
  id: 'g1',
  roomId: 'room',
  kind: 'theme',
  state: 'open',
  startedBy: 'ann',
  startedAt: at(0),
  closesAt: at(180),
  guests: true,
  scores: 'board',
  points: [],
  entries: [],
  ...over,
})

const entry = (userId: string, itemId: string): GameEntry => ({ userId, itemId, title: `Song ${itemId}`, played: false })

const match = (over: Partial<GameMatch> = {}): GameMatch => ({
  a: -1,
  b: -1,
  winner: -1,
  state: 'waiting',
  heartsA: 0,
  heartsB: 0,
  bye: false,
  walkover: false,
  toss: false,
  ...over,
})

const item = (id: string, over: Partial<QueueItem> = {}): QueueItem =>
  ({
    id,
    addedBy: 'ann',
    state: 'queued',
    lanePosition: 1,
    addedAt: at(0),
    track: { provider: 'fake', trackId: id, title: id, artists: [], durationMs: 1000, explicit: false },
    ...over,
  }) as QueueItem

describe('queue games', () => {
  it('are only at game night, and only those switched on', () => {
    const g = { level: 'gamenight', enabled: { connect: true, theme: false, bracket: true } } as unknown as RoomGames
    expect(queueGamesOn(g)).toEqual(['connect', 'bracket'])
    expect(queueGamesOn({ ...g, level: 'rounds' })).toEqual([])
  })

  it('find your team’s chain, or the room’s', () => {
    const connect = {
      from: 'Abba',
      to: 'Devo',
      hops: 3,
      teams: { bob: 1 },
      misses: [],
      chains: [
        { links: [], hints: [], done: false },
        { links: [{ userId: 'bob', itemId: 'i', title: 't', artist: 'Blondie', score: 0.5, points: 200 }], hints: [], done: false },
      ],
    }
    expect(chainFor(game({ kind: 'connect', connect }), 'bob')).toMatchObject({ team: 1, last: 'Blondie', teams: 2 })
    // Not on a team yet in a race.
    expect(chainFor(game({ kind: 'connect', connect }), 'ann')).toMatchObject({ team: undefined, last: 'Abba' })
    // Without teams, everyone's on the room's chain.
    expect(chainFor(game({ kind: 'connect', connect: { ...connect, teams: {}, chains: [connect.chains[1]] } }), 'ann')).toMatchObject({ team: 0, last: 'Blondie' })
    expect(closeness(0.8)).toBe('very close')
    expect(closeness(0.1)).toBe('a stretch')
  })

  it('count the entries you have left', () => {
    expect(entriesLeft(game(), 'ann')).toBe(1)
    expect(entriesLeft(game({ entries: [entry('ann', 'a')] }), 'ann')).toBe(0)
    const bracket = { size: 4, short: false, rounds: [] }
    expect(entriesLeft(game({ kind: 'bracket', bracket, entries: [entry('ann', 'a')] }), 'ann')).toBe(1)
    expect(entriesLeft(game({ kind: 'bracket', bracket, entries: [entry('bob', 'b'), entry('bob', 'c'), entry('eve', 'd')] }), 'ann')).toBe(1)
    expect(entriesLeft(game({ kind: 'bracket', bracket, state: 'playing' }), 'ann')).toBe(0)
  })

  it('offer your waiting songs not already entered', () => {
    const items = [item('a'), item('b'), item('c', { addedBy: 'bob' }), item('d', { state: 'played' }), item('e', { heldForGame: true })]
    expect(enterable(items, [game({ entries: [entry('ann', 'b')] })], 'ann').map((i) => i.id)).toEqual(['a'])
  })

  it('name a bracket’s rounds and matches', () => {
    expect([0, 1, 2, 3].map((r) => roundName(r, 4))).toEqual(['Round 1', 'Quarterfinals', 'Semifinals', 'Final'])
    const g = game({
      kind: 'bracket',
      state: 'playing',
      entries: [entry('ann', 'a'), entry('bob', 'b'), entry('eve', 'c')],
      bracket: {
        size: 4,
        short: false,
        rounds: [[match({ a: 2, bye: true, winner: 2, state: 'done' }), match({ a: 0, b: 1, winner: 1, state: 'done' })], [match({ a: 2, b: 1, state: 'up' })]],
        current: { round: 1, match: 0 },
        last: { round: 0, match: 1 },
      },
    })
    expect(currentMatch(g)).toMatchObject({ round: 1, index: 0, name: 'Final', a: { itemId: 'c' }, b: { itemId: 'b' } })
    expect(lastMatch(g)).toMatchObject({ round: 0, index: 1, winner: { itemId: 'b' } })
    expect(champion(g)).toBeUndefined()
    expect(champion({ ...g, bracket: { ...g.bracket!, champion: 2 } })?.itemId).toBe('c')
  })

  it('may be moved on by their starter, the owner or an admin', () => {
    expect(mayClose(game(), { id: 'ann' }, 'owner')).toBe(true)
    expect(mayClose(game(), { id: 'owner' }, 'owner')).toBe(true)
    expect(mayClose(game(), { id: 'eve', role: 'admin' }, 'owner')).toBe(true)
    expect(mayClose(game(), { id: 'bob', role: 'member' }, 'owner')).toBe(false)
  })

  it('come down from the cache once done', () => {
    const qc = new QueryClient()
    putQueueGame(qc, game({ id: 'b', kind: 'bracket', startedAt: at(5) }))
    putQueueGame(qc, game({ id: 'a' }))
    expect(qc.getQueryData(queueGamesQuery('room').queryKey)?.map((g) => g.id)).toEqual(['a', 'b'])
    putQueueGame(qc, game({ id: 'a', state: 'done' }))
    expect(qc.getQueryData(queueGamesQuery('room').queryKey)?.map((g) => g.id)).toEqual(['b'])
  })
})
