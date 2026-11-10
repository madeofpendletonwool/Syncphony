// The server's clock, as seen from here. Playback positions are stamped
// with server time; a phone or TV whose clock is a few seconds off would
// otherwise show a different lyric line from everyone else, and a clock
// that's a tenth of a second off puts its beat visibly out of step. The
// room socket's hello gives a rough offset at once; measureServerClock
// then times a few round trips to /time for a close one.

import { api } from '@/api/client'

/** Offsets from the hello smaller than this are network latency, not a wrong clock. */
const NOISE_MS = 250

let offset = 0
// Whether offset came from measured round trips, which the hello's rough one mustn't overwrite.
let measured = false

/** Records the server's time, as a hello received at `receivedAt` gave it. */
export function syncServerClock(serverTime: string, receivedAt = Date.now()) {
  const t = Date.parse(serverTime)
  if (!Number.isFinite(t) || measured) return
  const d = t - receivedAt
  offset = Math.abs(d) < NOISE_MS ? 0 : d
}

/** One round trip: when it was sent and came back (our clock), and the server's time in between. */
export type ClockSample = { sent: number; received: number; server: number }

/**
 * The offset the samples give: the quickest round trip's, which has the
 * least room for the server's answer to sit anywhere but its middle.
 */
export function offsetFrom(samples: ClockSample[]): number | null {
  const ok = samples.filter((s) => Number.isFinite(s.server) && s.received >= s.sent)
  if (!ok.length) return null
  const best = ok.reduce((a, b) => (b.received - b.sent < a.received - a.sent ? b : a))
  return Math.round(best.server - (best.sent + best.received) / 2)
}

let measuring: Promise<void> | null = null

/** Measures the offset over a few round trips to the server. Safe to call often: one runs at a time. */
export function measureServerClock(rounds = 5) {
  measuring ??= (async () => {
    const samples: ClockSample[] = []
    for (let i = 0; i < rounds; i++) {
      const sent = Date.now()
      const { data } = await api.GET('/time', { signal: AbortSignal.timeout(3000) }).catch(() => ({ data: undefined }))
      const received = Date.now()
      if (data) samples.push({ sent, received, server: data.ms })
    }
    const o = offsetFrom(samples)
    if (o !== null) {
      offset = o
      measured = true
    }
  })().finally(() => {
    measuring = null
  })
  return measuring
}

/** How far the server's clock is ahead of ours, in ms. */
export function serverOffset() {
  return offset
}

/** The server's time now, in ms since the epoch. */
export function serverNow(now = Date.now()) {
  return now + offset
}

/** A server timestamp on our clock. */
export function toLocalTime(serverMs: number) {
  return serverMs - offset
}
