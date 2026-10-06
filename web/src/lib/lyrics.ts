import { queryOptions } from '@tanstack/react-query'
import { useSyncExternalStore } from 'react'
import { api } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

// Synced lyrics (MAD-715). The server finds them (MAD-712); every screen
// works out the current line from the room's playback position, so the
// whole room reads along in step.

export type Lyrics = components['schemas']['Lyrics']
export type LyricLine = components['schemas']['LyricLine']

/** A queued song's lyrics, or null when there are none. */
export const lyricsQuery = (roomId: string, itemId: string) =>
  queryOptions({
    queryKey: ['lyrics', roomId, itemId],
    queryFn: async () => {
      try {
        return await unwrap(api.GET('/rooms/{roomId}/queue/{itemId}/lyrics', { params: { path: { roomId, itemId } } }))
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null
        throw e
      }
    },
    staleTime: Infinity,
    retry: (n, e) => !(e instanceof ApiError && e.status < 500) && n < 2,
  })

/**
 * The line being sung at positionMs: the last one that has started, or -1
 * before the first. Lines are in order, so this is a binary search.
 */
export function activeLine(lines: LyricLine[], positionMs: number) {
  let lo = 0
  let hi = lines.length - 1
  let found = -1
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (lines[mid].atMs <= positionMs) {
      found = mid
      lo = mid + 1
    } else {
      hi = mid - 1
    }
  }
  return found
}

/** How far into its line positionMs is, 0 to 1. */
export function lineProgress(lines: LyricLine[], i: number, positionMs: number, durationMs: number) {
  if (i < 0) return 0
  const start = lines[i].atMs
  const end = i + 1 < lines.length ? lines[i + 1].atMs : Math.max(start + 4000, durationMs)
  return Math.min(1, Math.max(0, (positionMs - start) / Math.max(1, end - start)))
}

/**
 * A gap: no words for a while at positionMs (an intro, a solo, a long
 * pause marked by an empty line). Big screens fill gaps with liner notes.
 */
export function inGap(lines: LyricLine[], positionMs: number, minGapMs = 10_000) {
  if (lines.length === 0) return false
  const i = activeLine(lines, positionMs)
  const next = lines[i + 1]?.atMs ?? Infinity
  const blank = i < 0 || lines[i].text.trim() === ''
  if (!blank) return false
  // Already in it long enough to settle, and long enough left to read a card.
  const since = i < 0 ? positionMs : positionMs - lines[i].atMs
  return since > 1500 && next - positionMs > Math.min(minGapMs, 6000) && next - (i < 0 ? 0 : lines[i].atMs) >= minGapMs
}

// --- Sync offset --------------------------------------------------------------
//
// Some LRC files are a little early or late. Anyone can nudge a song's
// lyrics on their own device; it's remembered per song.

const OFFSETS_KEY = 'syncphony-lyrics-offsets'
const MAX_OFFSETS = 200
export const OFFSET_STEP_MS = 250
export const MAX_OFFSET_MS = 10_000

type Offsets = Record<string, number>

function read(): Offsets {
  try {
    const v = JSON.parse(localStorage.getItem(OFFSETS_KEY) ?? '{}') as unknown
    return v && typeof v === 'object' ? (v as Offsets) : {}
  } catch {
    return {}
  }
}

let offsets: Offsets = read()
const listeners = new Set<() => void>()

/** The key a song's offset is kept under. */
export function offsetKey(track: { provider: string; trackId: string }) {
  return `${track.provider}:${track.trackId}`
}

export function getOffset(key: string) {
  return offsets[key] ?? 0
}

/** Sets a song's offset: positive shows lines later, negative earlier. */
export function setOffset(key: string, ms: number) {
  const v = Math.max(-MAX_OFFSET_MS, Math.min(MAX_OFFSET_MS, Math.round(ms)))
  const next = { ...offsets }
  delete next[key]
  if (v !== 0) next[key] = v
  // Keep the most recent few hundred: insertion order is age.
  const keys = Object.keys(next)
  for (const k of keys.slice(0, Math.max(0, keys.length - MAX_OFFSETS))) delete next[k]
  offsets = next
  try {
    localStorage.setItem(OFFSETS_KEY, JSON.stringify(offsets))
  } catch {
    // Private mode: it lasts until reload.
  }
  listeners.forEach((l) => l())
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

/** A song's lyrics offset, and a setter. */
export function useLyricsOffset(key: string | undefined) {
  const ms = useSyncExternalStore(subscribe, () => (key ? getOffset(key) : 0))
  return [ms, (v: number) => key && setOffset(key, v)] as const
}
