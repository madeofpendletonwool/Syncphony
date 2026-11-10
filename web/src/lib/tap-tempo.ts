import { beatAt, type BeatMap } from './beat-map'

// Tap tempo for the beat lab: turning a few taps on the beat into either a
// grid of its own, or a correction to the song's analysed beats.

/** A straight line through the taps: the beat's length, and when beat 0 (the first tap) fell. */
export type TapFit = { period: number; firstMs: number }

/**
 * Fits taps (song positions, ms, in order) to evenly spaced beats. People
 * don't tap perfectly: a tap that comes a beat late counts the beat it
 * skipped, and a double tap (two within half a beat) counts once. Null
 * with fewer than four usable taps.
 */
export function fitTaps(times: number[]): TapFit | null {
  if (times.length < 4) return null
  const gaps = times.slice(1).map((t, i) => t - times[i])
  const typical = median(gaps)
  if (!(typical > 0)) return null
  // Drop double taps, then take the typical gap again without them.
  const kept = [times[0]]
  for (const t of times.slice(1)) if (t - kept[kept.length - 1] >= typical * 0.5) kept.push(t)
  if (kept.length < 4) return null
  const period0 = median(kept.slice(1).map((t, i) => t - kept[i]))
  // Each tap's beat: the last one's, plus however many beats the gap spans.
  const idx = [0]
  for (let i = 1; i < kept.length; i++) idx.push(idx[i - 1] + Math.max(1, Math.round((kept[i] - kept[i - 1]) / period0)))
  // Least squares: tap i lands at first + idx[i]·period.
  const n = kept.length
  const mi = idx.reduce((a, b) => a + b, 0) / n
  const mt = kept.reduce((a, b) => a + b, 0) / n
  let num = 0
  let den = 0
  kept.forEach((t, i) => {
    num += (idx[i] - mi) * (t - mt)
    den += (idx[i] - mi) ** 2
  })
  if (den === 0) return null
  const period = num / den
  if (!(period > 0)) return null
  return { period, firstMs: mt - mi * period }
}

/** How the taps move the song's analysed beats: later by offsetMs, and the bar starting barShift beats on. */
export type MapShift = { offsetMs: number; barShift: number }

/**
 * When the taps keep the analysed tempo (or half of it, tapping every
 * other beat), they only say where the beat really is. Keeps the map's own
 * beats, which follow a live band's drift that one fixed grid can't, and
 * works out how far they're off. The first tap is a "one". Null when the
 * taps are at another tempo: the map's tempo is wrong, so a grid is better.
 */
export function alignToMap(map: BeatMap, times: number[], period: number): MapShift | null {
  if (map.bpm <= 0 || times.length === 0) return null
  const beats = (period * map.bpm) / 60000
  if (Math.abs(beats - 1) > 0.06 && Math.abs(beats - 2) > 0.12) return null
  // Each tap's distance from its nearest beat, as a share of that beat.
  const phases: number[] = []
  for (const t of times) {
    const p = beatAt(map, t)
    if (p) phases.push(p.into / p.period)
  }
  if (phases.length < 3) return null
  // Phases wrap (0.95 and 0.05 are close), so centre them on their
  // circular mean before taking the median, which ignores a stray tap.
  const x = phases.reduce((a, f) => a + Math.cos(2 * Math.PI * f), 0)
  const y = phases.reduce((a, f) => a + Math.sin(2 * Math.PI * f), 0)
  const centre = Math.atan2(y, x) / (2 * Math.PI)
  const wrap = (f: number) => f - Math.round(f)
  const phase = centre + median(phases.map((f) => wrap(f - centre)))
  const shifted = wrap(phase)
  // In ms, at the tempo around the taps.
  const near = beatAt(map, times[0])
  const offsetMs = shifted * (near?.period ?? 60000 / map.bpm)
  // The map beat the first tap is on becomes the one.
  const first = beatAt(map, times[0] - offsetMs)
  if (!first) return { offsetMs, barShift: 0 }
  const index = first.into > first.period / 2 ? first.index + 1 : first.index
  return { offsetMs, barShift: mod4(index - map.downbeat) }
}

export function mod4(n: number) {
  return ((n % 4) + 4) % 4
}

function median(xs: number[]) {
  if (xs.length === 0) return NaN
  const s = [...xs].sort((a, b) => a - b)
  const m = s.length >> 1
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2
}
