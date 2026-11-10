// Evening out the volume from song to song. The server measures each
// song's loudness (LUFS, in its beat map); the speaker turns loud songs
// down to TARGET_LUFS, so a 60s single and a modern master sit together.
//
// An <audio> element can only turn down (volume tops out at 1), so songs
// quieter than the target stay as they are. -16 LUFS (Apple Music's level)
// is under most of a typical library, older records included: the room
// plays a little quieter overall, and most songs come out the same.

export const TARGET_LUFS = -16
/** The most a song is turned down, dB: a measurement that's way off can't make it inaudible. */
const MAX_CUT_DB = 12

/** The volume (0–1) that brings a song of this loudness to the target; 1 if it isn't known. */
export function volumeFor(lufs: number | undefined | null) {
  if (typeof lufs !== 'number' || !Number.isFinite(lufs) || lufs <= TARGET_LUFS) return 1
  return 10 ** (-Math.min(lufs - TARGET_LUFS, MAX_CUT_DB) / 20)
}

/**
 * Moves el's volume to `to`: at once before the song is under way, else
 * over a couple of seconds, so a map that arrives late doesn't jolt.
 * Returns the cancel.
 */
export function rampVolume(el: HTMLMediaElement, to: number, ms = 2000) {
  const from = el.volume
  if (el.paused || el.currentTime < 0.5 || Math.abs(to - from) < 0.01) {
    el.volume = to
    return () => {}
  }
  const start = performance.now()
  const t = setInterval(() => {
    const k = Math.min(1, (performance.now() - start) / ms)
    el.volume = from + (to - from) * k
    if (k >= 1) clearInterval(t)
  }, 50)
  return () => clearInterval(t)
}
