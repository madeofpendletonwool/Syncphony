// The server's clock, as seen from here. Playback positions are stamped
// with server time; a phone or TV whose clock is a few seconds off would
// otherwise show a different lyric line from everyone else. The room
// socket's hello carries the server's time, and this keeps the offset.

/** Offsets smaller than this are network latency, not a wrong clock. */
const NOISE_MS = 250

let offset = 0

/** Records the server's time, as a hello received at `receivedAt` gave it. */
export function syncServerClock(serverTime: string, receivedAt = Date.now()) {
  const t = Date.parse(serverTime)
  if (!Number.isFinite(t)) return
  const d = t - receivedAt
  offset = Math.abs(d) < NOISE_MS ? 0 : d
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
