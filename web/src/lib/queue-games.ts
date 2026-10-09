import { queryOptions, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import type { QueueItem } from './playback'
import type { RoomGames } from './games'

// Queue games (MAD-794..796, ADR 0015): connect the artists, theme rounds
// and bracket battles, played through the queue across songs, beside the
// rounds. The server runs them; the room socket keeps them in the cache.

export type QueueGame = components['schemas']['QueueGame']
export type QueueGameKind = QueueGame['kind']
export type ThemeKind = components['schemas']['ThemeKind']
export type GameEntry = components['schemas']['GameEntry']
export type GameMatch = components['schemas']['GameMatch']
export type StartQueueGame = components['schemas']['StartQueueGameRequest']

/** Each queue game, as menus and screens name it. */
export const QUEUE_GAMES: Record<QueueGameKind, { label: string; hint: string }> = {
  connect: { label: 'Connect the artists', hint: 'Link one artist to another, a song at a time' },
  theme: { label: 'Theme round', hint: 'Everyone queues a song to a prompt; the room hearts its favourite' },
  bracket: { label: 'Bracket battle', hint: 'Songs face off through the night; the room picks each winner' },
}

/** Themes a host can pick, by kind. */
export const THEMES: { id: ThemeKind; label: string }[] = [
  { id: 'before_1985', label: 'Before 1985' },
  { id: 'nineties', label: 'The 90s' },
  { id: 'year', label: 'A year' },
  { id: 'cover', label: 'A cover' },
  { id: 'samples', label: 'Samples something' },
  { id: 'live', label: 'Live' },
  { id: 'producer', label: 'A producer' },
  { id: 'fast', label: 'Over 140 BPM' },
  { id: 'slow', label: 'Slow' },
  { id: 'build', label: 'A big build' },
  { id: 'sad', label: 'Sad' },
  { id: 'genre', label: 'A genre' },
]

export const BRACKET_SIZES = [4, 8, 16] as const

/** The queue games a room's settings turn on. */
export function queueGamesOn(games: RoomGames): QueueGameKind[] {
  if (games.level !== 'gamenight') return []
  return (['connect', 'theme', 'bracket'] as const).filter((k) => !!games.enabled[k])
}

export const queueGamesQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['queue-games', roomId],
    queryFn: async () => (await unwrap(api.GET('/rooms/{roomId}/games/queue', { params: { path: { roomId } } }))).games,
    staleTime: Infinity,
  })

/** Puts a `game.queue` event in the cache: a game that's done comes down. */
export function putQueueGame(queryClient: QueryClient, game: QueueGame) {
  queryClient.setQueryData(queueGamesQuery(game.roomId).queryKey, (old) => {
    const rest = (old ?? []).filter((g) => g.id !== game.id)
    return game.state === 'done' ? rest : [...rest, game].sort((a, b) => a.startedAt.localeCompare(b.startedAt))
  })
}

export function startQueueGame(roomId: string, body: StartQueueGame) {
  return unwrap(api.POST('/rooms/{roomId}/games/queue', { params: { path: { roomId } }, body }))
}

export function enterQueueGame(roomId: string, gameId: string, itemId: string) {
  return unwrap(api.POST('/rooms/{roomId}/games/queue/{gameId}/entries', { params: { path: { roomId, gameId } }, body: { itemId } }))
}

export function withdrawEntry(roomId: string, gameId: string, itemId: string) {
  return unwrap(api.DELETE('/rooms/{roomId}/games/queue/{gameId}/entries/{itemId}', { params: { path: { roomId, gameId, itemId } } }))
}

export function hintQueueGame(roomId: string, gameId: string) {
  return unwrap(api.POST('/rooms/{roomId}/games/queue/{gameId}/hint', { params: { path: { roomId, gameId } } }))
}

export function closeQueueGame(roomId: string, gameId: string) {
  return unwrap(api.POST('/rooms/{roomId}/games/queue/{gameId}/close', { params: { path: { roomId, gameId } } }))
}

// --- Connect the artists ---------------------------------------------------------

/** Someone's team's chain, and where it ends; the room's chain without teams. */
export function chainFor(game: QueueGame, userId: string) {
  const c = game.connect
  if (!c) return undefined
  const team = c.teams[userId] ?? (c.chains.length === 1 ? 0 : undefined)
  const chain = team === undefined ? undefined : c.chains[team]
  const last = chain?.links.at(-1)?.artist ?? c.from
  return { team, chain, last, teams: c.chains.length }
}

/** A team's name. */
export function teamName(team: number) {
  return `Team ${'AB'[team] ?? team + 1}`
}

/** How alike a link's artists are, in words. */
export function closeness(score: number) {
  if (score >= 0.66) return 'very close'
  if (score >= 0.33) return 'close'
  return 'a stretch'
}

// --- Theme rounds and brackets -------------------------------------------------

/** Your entries in a game. */
export function myEntries(game: QueueGame, userId: string) {
  return game.entries.filter((e) => e.userId === userId)
}

/** How many songs you may still enter. */
export function entriesLeft(game: QueueGame, userId: string) {
  if (game.state !== 'open' || game.kind === 'connect') return 0
  const mine = myEntries(game, userId).length
  if (game.kind === 'theme') return mine === 0 ? 1 : 0
  return Math.max(0, Math.min(2 - mine, (game.bracket?.size ?? 0) - game.entries.length))
}

/**
 * Your songs you could enter: waiting in your lane, and not entered in
 * any game already. A theme round swaps your entry for another.
 */
export function enterable(items: QueueItem[], games: QueueGame[], userId: string) {
  const entered = new Set(games.flatMap((g) => g.entries.map((e) => e.itemId)))
  return items.filter((i) => i.addedBy === userId && !i.autopilot && i.state === 'queued' && !i.heldForGame && !entered.has(i.id))
}

/** The fit marks: ✓ fits, ✗ doesn't, ? no telling. */
export const FIT_MARK: Record<NonNullable<GameEntry['fit']>, string> = { yes: '✓', no: '✗', unknown: '?' }

/** A bracket round's name, counted from the final. */
export function roundName(round: number, rounds: number) {
  const fromEnd = rounds - 1 - round
  if (fromEnd === 0) return 'Final'
  if (fromEnd === 1) return 'Semifinals'
  if (fromEnd === 2) return 'Quarterfinals'
  return `Round ${round + 1}`
}

/** The match up now, with its two entries. */
export function currentMatch(game: QueueGame) {
  const b = game.bracket
  if (!b?.current) return undefined
  const m = b.rounds[b.current.round]?.[b.current.match]
  if (!m || m.a < 0 || m.b < 0) return undefined
  const { round, match: index } = b.current
  return { round, index, match: m, a: game.entries[m.a], b: game.entries[m.b], name: roundName(round, b.rounds.length) }
}

/** The match decided last, with its winner, for screens to celebrate. */
export function lastMatch(game: QueueGame) {
  const b = game.bracket
  if (!b?.last) return undefined
  const m = b.rounds[b.last.round]?.[b.last.match]
  if (!m || m.winner < 0) return undefined
  const { round, match: index } = b.last
  return { round, index, match: m, winner: game.entries[m.winner], name: roundName(round, b.rounds.length) }
}

/** The bracket's champion's entry, once there is one. */
export function champion(game: QueueGame) {
  const i = game.bracket?.champion
  return i === undefined ? undefined : game.entries[i]
}

/** Whether someone may move a game on: its starter, the room's owner, or an admin. */
export function mayClose(game: QueueGame, me: { id: string; role?: string }, ownerId: string) {
  return game.startedBy === me.id || ownerId === me.id || me.role === 'admin'
}
