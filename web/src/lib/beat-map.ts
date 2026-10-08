import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'

// Beat maps (MAD-773): the server works out each song's beats, bars,
// sections and spectrum once, from its audio. This loads them and answers
// "where in the music are we?" for the beat engine (lib/beat.ts).

type Wire = components['schemas']['BeatMap']

export type BeatMap = Omit<Wire, 'bands' | 'loudness'> & {
  bands: Uint8Array
  loudness: Uint8Array
}

const loaded = new Map<string, Promise<BeatMap | null>>()

/** A queued song's beat map, or null if it can't have one. Cached per item. */
export function loadBeatMap(roomId: string, itemId: string): Promise<BeatMap | null> {
  const key = `${roomId}/${itemId}`
  let hit = loaded.get(key)
  if (!hit) {
    hit = unwrap(api.GET('/rooms/{roomId}/queue/{itemId}/beatmap', { params: { path: { roomId, itemId } } }))
      .then(decode)
      .catch(() => {
        // A miss now (no map, or the server was busy) may be a hit later.
        setTimeout(() => loaded.delete(key), 60_000)
        return null
      })
    loaded.set(key, hit)
    // Keep the last few songs' maps; they're up to a few hundred KB.
    while (loaded.size > 8) loaded.delete(loaded.keys().next().value!)
  }
  return hit
}

export function decode(w: Wire): BeatMap {
  return { ...w, bands: base64(w.bands), loudness: base64(w.loudness) }
}

function base64(s: string) {
  const bin = atob(s)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

/** Where pos (ms) falls among the beats. */
export type BeatPlace = {
  /** Beats since the first, and ms into this one. */
  index: number
  into: number
  /** This beat's length, ms. */
  period: number
  /** Its place in the bar, 0 for the one. */
  bar: number
}

/** The beat at pos, or null before the first beat and after the last. */
export function beatAt(m: BeatMap, pos: number): BeatPlace | null {
  const b = m.beats
  if (b.length < 2 || pos < b[0] || pos >= b[b.length - 1] + (b[b.length - 1] - b[b.length - 2])) return null
  // The last beat at or before pos.
  let lo = 0
  let hi = b.length - 1
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1
    if (b[mid] <= pos) lo = mid
    else hi = mid - 1
  }
  const period = lo + 1 < b.length ? b[lo + 1] - b[lo] : b[lo] - b[lo - 1]
  return { index: lo, into: pos - b[lo], period, bar: (((lo - m.downbeat) % 4) + 4) % 4 }
}

/** The section energy at pos, 0–1. */
export function sectionEnergy(m: BeatMap, pos: number) {
  let e = m.sections[0]?.energy ?? 0.5
  for (const s of m.sections) {
    if (s.startMs > pos) break
    e = s.energy
  }
  return e
}

/** Each band's level at pos, 0–1, into out (bandCount long). */
export function spectrumAt(m: BeatMap, pos: number, out: Float32Array) {
  const f = (pos / 1000) * m.frameRate
  const i = Math.floor(f)
  const frames = m.loudness.length
  if (i < 0 || i >= frames) {
    out.fill(0)
    return out
  }
  const j = Math.min(i + 1, frames - 1)
  const t = f - i
  for (let k = 0; k < m.bandCount; k++) {
    out[k] = (m.bands[i * m.bandCount + k] * (1 - t) + m.bands[j * m.bandCount + k] * t) / 255
  }
  return out
}

/** The overall level at pos, 0–1. */
export function loudnessAt(m: BeatMap, pos: number) {
  const i = Math.floor((pos / 1000) * m.frameRate)
  return i >= 0 && i < m.loudness.length ? m.loudness[i] / 255 : 0
}

/**
 * The song's shape for the waveform: `bars` heights, 0–1. Each bar is the
 * average level of its stretch, scaled against the song's own quiet and
 * loud parts, so a loud, compressed song still shows its verses and
 * choruses instead of sitting at the top. A song that barely changes sits
 * mid-height, so small wobbles don't become big ones.
 */
export function waveform(loudness: Uint8Array, bars: number): number[] {
  const per = loudness.length / bars
  const means: number[] = []
  for (let b = 0; b < bars; b++) {
    const from = Math.floor(b * per)
    const to = Math.max(from + 1, Math.min(loudness.length, Math.floor((b + 1) * per)))
    let sum = 0
    for (let i = from; i < to; i++) sum += loudness[i] ?? 0
    means.push(sum / (to - from) / 255)
  }
  const sorted = [...means].sort((a, b) => a - b)
  let hi = sorted[sorted.length - 1] ?? 0
  let lo = sorted[Math.floor(sorted.length * 0.1)] ?? 0
  if (hi - lo < 0.08) {
    // Barely changes: sit in the middle, wobbling a little.
    const mid = (hi + lo) / 2
    lo = mid - 0.04
    hi = mid + 0.04
  }
  // Quiet parts still show a sliver, and only the loudest bar reaches the top.
  return means.map((m) => 0.12 + 0.8 * Math.min(1, Math.max(0, (m - lo) / (hi - lo))) ** 1.5)
}
