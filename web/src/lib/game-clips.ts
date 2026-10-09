import { serverNow } from './clock'
import type { GameRound } from './games'
import { speakerState } from './speaker'

// Name that tune's clips (MAD-792, MAD-793). The server stops the room's
// music and lists each clip in the round once it's time to play it; every
// device playing the room (the speaker, and listeners) plays it then. The
// clips' URLs are opaque, so nothing here names the song.

type Clip = NonNullable<GameRound['clips']>[number]

/** A clip's address. */
export function clipUrl(roomId: string, clipId: string) {
  return `/api/rooms/${encodeURIComponent(roomId)}/games/clips/${encodeURIComponent(clipId)}`
}

// A clip that's this long over isn't worth starting late.
const STALE = 1_500

/**
 * The clip to play now: the latest the round has out that hasn't been
 * played here, unless it's long over. Older ones are skipped, never played
 * late, so a phone that dozed off doesn't fall behind.
 */
export function nextClip(round: GameRound, played: ReadonlySet<string>, now = serverNow()): Clip | undefined {
  const out = round.clips ?? []
  const latest = out[out.length - 1]
  if (!latest || played.has(latest.id)) return undefined
  if (now > Date.parse(latest.at) + latest.lengthMs + STALE) return undefined
  return latest
}

const played = new Set<string>()
let el: HTMLAudioElement | undefined

/** Plays a round's newest clip, if this device plays the room. */
export function playRoundClips(round: GameRound) {
  const speaker = speakerState.get()
  if (speaker.status === 'off' || speaker.roomId !== round.roomId) return
  const clip = nextClip(round, played)
  for (const c of round.clips ?? []) played.add(c.id)
  if (!clip) return
  el ??= new Audio()
  el.src = clipUrl(round.roomId, clip.id)
  // Blocked until someone taps: the room hears the next one.
  el.play().catch(() => undefined)
}
