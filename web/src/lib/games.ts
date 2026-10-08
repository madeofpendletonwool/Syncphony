import { queryOptions, useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { serverNow } from './clock'
import type { Room } from './room'
import { createStore, useStore } from './store'

// Music party games (Phase 10, ADR 0015). The server runs every round;
// screens show it and phones send answers. The room socket keeps the
// round and tonight's scores in the query cache.

export type GameRound = components['schemas']['GameRound']
export type GameScores = components['schemas']['GameScores']
export type GameKind = components['schemas']['GameKind']
export type Award = components['schemas']['Award']
export type RoomGames = Room['games']
export type GameLevel = RoomGames['level']
export type GamesChange = components['schemas']['RoomGamesChange']

/** The levels, each in plain words. */
export const LEVELS: { id: GameLevel; label: string; hint: string }[] = [
  { id: 'off', label: 'Off', hint: 'No games. Just the music.' },
  { id: 'recap', label: 'Recap', hint: 'Recap only: awards when the night ends. Nothing in between.' },
  { id: 'ambient', label: 'Ambient', hint: 'Ambient: little questions you can ignore. The music never stops.' },
  { id: 'rounds', label: 'Rounds', hint: 'Rounds: some songs become a round, with a reveal on the big screen.' },
  { id: 'gamenight', label: 'Game night', hint: 'Game night: dedicated rounds and queue games. Some pause the music.' },
]

/** Each game, as the settings list it. */
export const GAMES: Record<GameKind, { label: string; hint: string }> = {
  year: { label: 'Guess the year', hint: 'When did it first come out? And older or newer than the last?' },
  liner: { label: 'Liner notes', hint: 'Covers, credits and releases' },
  sample: { label: 'Sample detective', hint: 'What it samples, and what samples it' },
  lyrics: { label: 'Beat the singer', hint: 'Fill in a line’s missing words before it’s sung' },
  finish_lyric: { label: 'Finish the lyric', hint: 'The music stops; you finish the line' },
  tune: { label: 'Name that tune', hint: 'From a few seconds of the song' },
  connect: { label: 'Connect the artists', hint: 'Link one artist to another through the queue' },
  theme: { label: 'Theme rounds', hint: 'Everyone queues to a theme' },
  bracket: { label: 'Bracket battles', hint: 'Songs face off, the room picks' },
}

/** Whether a level runs rounds (rather than nothing, or just awards). */
export function plays(level: GameLevel) {
  return level === 'ambient' || level === 'rounds' || level === 'gamenight'
}

export const gameRoundQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['game-round', roomId],
    // 204 when no round is up.
    queryFn: async (): Promise<GameRound | null> =>
      (await unwrap(api.GET('/rooms/{roomId}/games/round', { params: { path: { roomId } } }))) ?? null,
    staleTime: Infinity,
  })

export const gameScoresQuery = (roomId: string) =>
  queryOptions({
    queryKey: ['game-scores', roomId],
    queryFn: () => unwrap(api.GET('/rooms/{roomId}/games/scores', { params: { path: { roomId } } })),
    staleTime: Infinity,
  })

export function startRound(roomId: string, kind?: GameKind) {
  return unwrap(api.POST('/rooms/{roomId}/games/rounds', { params: { path: { roomId } }, body: kind ? { kind } : {} }))
}

export type GameAnswer = components['schemas']['GameAnswerRequest']

export function answerRound(roomId: string, roundId: string, body: GameAnswer) {
  return unwrap(api.POST('/rooms/{roomId}/games/rounds/{roundId}/answers', { params: { path: { roomId, roundId } }, body }))
}

/** What you answered each round, by round ID, so the sheet shows it. */
export const myAnswers = createStore<Record<string, GameAnswer>>({})

export function rememberAnswer(roundId: string, a: GameAnswer) {
  myAnswers.set((m) => ({ ...m, [roundId]: a }))
}

// --- This device's games ----------------------------------------------------

const MUTE_KEY = 'syncphony.games.muted'

function readMuted() {
  try {
    return localStorage.getItem(MUTE_KEY) === '1'
  } catch {
    return false
  }
}

/** Whether this device keeps game prompts to itself (remembered, like keeping the screen on). */
export const gamesMuted = createStore<boolean>(readMuted())

export function setGamesMuted(on: boolean) {
  gamesMuted.set(on)
  try {
    localStorage.setItem(MUTE_KEY, on ? '1' : '0')
  } catch {
    // Private mode: lasts until reload.
  }
}

export function useGamesMuted() {
  return useStore(gamesMuted)
}

// --- Hiding answers ---------------------------------------------------------

/** Whether a round keeps something about a song back until its reveal. */
export function hides(round: GameRound | null | undefined, itemId: string | undefined, what: GameRound['hides'][number]) {
  return !!round && !!itemId && round.itemId === itemId && (round.state === 'announce' || round.state === 'open') && round.hides.includes(what)
}

/** What the room's round hides about a song right now. */
export function useHidden(roomId: string | undefined, itemId: string | undefined) {
  const round = useQuery({ ...gameRoundQuery(roomId ?? ''), enabled: !!roomId }).data
  return {
    song: hides(round, itemId, 'song'),
    notes: hides(round, itemId, 'notes'),
    lyrics: hides(round, itemId, 'lyrics'),
  }
}

// --- Lyric lines ------------------------------------------------------------

/** What stands in for a lyric line a round keeps back. */
export const HIDDEN_LINE = '• • •'

/** When the lyric line a round keeps back is sung, if it keeps one back. */
export function hiddenLineAt(round: GameRound | null | undefined, itemId: string | undefined) {
  return hides(round, itemId, 'line') ? round?.atMs : undefined
}

/** The lyric line the room's round keeps back right now, by when it's sung. */
export function useHiddenLine(roomId: string | undefined, itemId: string | undefined) {
  const round = useQuery({ ...gameRoundQuery(roomId ?? ''), enabled: !!roomId }).data
  return hiddenLineAt(round, itemId)
}

/**
 * Lyrics with a hidden line blanked out, everywhere it's sung (a chorus
 * comes back). The server does the same, for lyrics fetched during a round.
 */
export function maskLine<T extends { atMs: number; text: string }>(lines: T[], atMs: number | undefined): T[] {
  const text = atMs === undefined ? undefined : lines.find((l) => l.atMs === atMs)?.text.trim()
  if (!text) return lines
  return lines.map((l) => (l.text.trim() === text ? { ...l, text: HIDDEN_LINE } : l))
}

/** A quoted line in parts, with "____" as blanks to fill in. */
export function lineParts(text: string): { text: string; blank: boolean }[] {
  return text
    .split(/(_{3,})/)
    .filter((t) => t !== '')
    .map((t) => ({ text: t, blank: /^_{3,}$/.test(t) }))
}

/** A prompt's question, and the quoted line under it, if any. */
export function splitPrompt(prompt: string) {
  const [question, ...rest] = prompt.split('\n')
  return { question, line: rest.length > 0 ? rest.join(' ') : undefined }
}

// --- Reveals ----------------------------------------------------------------

/** The cover of a round's other song (a sample's), from its reveal on. */
export function roundArtworkUrl(round: GameRound, size = 300) {
  if (!round.other?.hasArtwork) return undefined
  return `/api/rooms/${encodeURIComponent(round.roomId)}/games/rounds/${encodeURIComponent(round.id)}/artwork?size=${size}`
}

/** Your higher-or-lower streak tonight, and your best. */
export function streakOf(scores: GameScores | undefined, userId: string) {
  const p = scores?.players.find((x) => x.userId === userId)
  return { streak: p?.streak ?? 0, best: p?.bestStreak ?? 0 }
}

/** Whether answers are open, by the server's clock. */
export function isOpen(round: GameRound, now = serverNow()) {
  return round.state === 'open' && now < Date.parse(round.closesAt)
}

/** Seconds until a server time, never below 0. */
export function secondsUntil(at: string, now = serverNow()) {
  return Math.max(0, Math.ceil((Date.parse(at) - now) / 1000))
}

/** How much of the answer window is left, 0–1. */
export function remaining(round: GameRound, now: number) {
  const opens = Date.parse(round.opensAt)
  const closes = Date.parse(round.closesAt)
  if (round.state === 'announce') return 1
  if (round.state !== 'open' || closes <= opens) return 0
  return Math.max(0, Math.min(1, (closes - now) / (closes - opens)))
}
