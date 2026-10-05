import { useSyncExternalStore } from 'react'
import type { components } from '@/api/schema.gen'

export type Track = components['schemas']['QueuedTrack']
export type User = components['schemas']['User']

/**
 * What the shell's mini-player and now-playing view show. The playback
 * engine (MAD-691) and the room view (MAD-695) feed this from the room
 * socket; the shell only renders it.
 */
export type NowPlaying = {
  /** The room and queue item playing; absent in the design demo. */
  roomId?: string
  itemId?: string
  track: Track
  /** A URL the browser can load; Track.artwork is a provider ref. */
  artworkUrl?: string
  /** Who queued it. Their lane color tints the player. */
  requester?: Pick<User, 'id' | 'displayName' | 'color' | 'avatar'>
  paused: boolean
  /** Position at `at` (ms since epoch); the UI extrapolates while playing. */
  positionMs: number
  at: number
}

export type PlayerCommands = {
  toggle?: () => void
  next?: () => void
  previous?: () => void
  seek?: (positionMs: number) => void
}

type State = { nowPlaying: NowPlaying | null; commands: PlayerCommands }

let state: State = { nowPlaying: null, commands: {} }
const listeners = new Set<() => void>()

export const player = {
  get: () => state,
  set(next: Partial<State>) {
    state = { ...state, ...next }
    listeners.forEach((l) => l())
  },
  subscribe(l: () => void) {
    listeners.add(l)
    return () => {
      listeners.delete(l)
    }
  },
}

export function usePlayer() {
  return useSyncExternalStore(player.subscribe, player.get, player.get)
}

/** Current position for a NowPlaying, extrapolated to `now`. */
export function positionAt(np: NowPlaying, now: number) {
  const pos = np.paused ? np.positionMs : np.positionMs + (now - np.at)
  return Math.max(0, Math.min(pos, np.track.durationMs))
}

export function formatDuration(ms: number | undefined) {
  if (ms === undefined || !Number.isFinite(ms)) return '–:––'
  const total = Math.floor(ms / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}:${s.toString().padStart(2, '0')}`
}
