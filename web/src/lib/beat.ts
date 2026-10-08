// The beat engine (MAD-775, prototype). One rAF loop turns the room's
// playback position into a few CSS custom properties, so the app can
// breathe with the song without re-rendering anything.
//
// The real thing reads a server-side beat map (MAD-773). Until then a song's
// beat grid comes from tapping along (see BeatLab); a song nobody tapped
// runs on the default tempo, so it's in time but not in phase.
//
// Variables, written only to the elements that use them (a var on :root
// would restyle the whole tree every frame):
//   --beat      0→1 on each beat, easing back down. Scaled by energy.
//   --downbeat  the same on the first beat of each bar, slower to fade.
//   --swell     a slow breath once a bar: rises over a beat, fades over the
//               rest. For big surfaces, where a per-beat flash reads as strobe.
//   --energy    how hard the song is going right now, slowly smoothed.
//   --b0..--b3  four synthetic "bands" for the equalizer.
// Elements also get data-beat-live while the engine drives them, so CSS
// can swap a canned animation for the live one.

import { player, positionAt } from './now-playing'
import { createStore } from './store'

export type BeatLevel = 'off' | 'subtle'

export type BeatSettings = {
  level: BeatLevel
  /** Tempo for songs with no grid of their own. */
  defaultBpm: number
  /** Stand-in for the beat map's energy envelope, 0–1. */
  energy: number
  /** Run on a free clock when nothing's playing, to try the look. */
  freeRun: boolean
  /** The backdrop scene, or 'auto' to pick one per song. */
  scene: string
}

export const beatSettings = createStore<BeatSettings>({
  level: 'subtle',
  defaultBpm: 120,
  energy: 0.5,
  freeRun: false,
  scene: 'auto',
})

/** A song's beat grid: its tempo and the position of one downbeat, in ms. */
export type Grid = { bpm: number; anchorMs: number }

const grids = new Map<string, Grid>()
export const gridVersion = createStore(0)

const FREE_KEY = '__free'
const freeStart = performance.now()

/** Where the music is now, and which song's grid applies. */
function clock(): { key: string; pos: number; playing: boolean } | null {
  const np = player.get().nowPlaying
  if (np && !np.paused) return { key: np.itemId ?? np.track.trackId, pos: positionAt(np, Date.now()), playing: true }
  if (beatSettings.get().freeRun) return { key: FREE_KEY, pos: performance.now() - freeStart, playing: true }
  if (np) return { key: np.itemId ?? np.track.trackId, pos: np.positionMs, playing: false }
  return null
}

export function currentGrid(): Grid {
  const c = clock()
  return (c && grids.get(c.key)) ?? { bpm: beatSettings.get().defaultBpm, anchorMs: 0 }
}

function setGrid(g: Grid) {
  const c = clock()
  if (!c) return
  grids.set(c.key, { bpm: Math.min(220, Math.max(50, g.bpm)), anchorMs: g.anchorMs })
  gridVersion.set((v) => v + 1)
  wake()
}

// --- Tap tempo ---------------------------------------------------------------

let taps: number[] = []

/**
 * A tap on the beat. Start on a "one": a straight line through the taps
 * gives the tempo, and the first tap becomes a downbeat.
 */
export function tap() {
  const c = clock()
  if (!c) return
  const last = taps.at(-1)
  if (last === undefined || c.pos - last > 2000 || c.pos < last) taps = []
  taps.push(c.pos)
  taps = taps.slice(-12)
  if (taps.length < 3) return taps.length
  // Least squares: tap i lands at anchor + i·period.
  const n = taps.length
  const mi = (n - 1) / 2
  const mt = taps.reduce((a, b) => a + b, 0) / n
  let num = 0
  let den = 0
  taps.forEach((t, i) => {
    num += (i - mi) * (t - mt)
    den += (i - mi) ** 2
  })
  const period = num / den
  // The first tap is beat 0; move the anchor to the bar of the last one.
  const first = mt - mi * period
  const bars = Math.floor((n - 1) / 4)
  setGrid({ bpm: 60000 / period, anchorMs: first + bars * 4 * period })
  return n
}

export function nudge(ms: number) {
  const g = currentGrid()
  setGrid({ ...g, anchorMs: g.anchorMs + ms })
}

export function scaleTempo(factor: number) {
  const g = currentGrid()
  setGrid({ ...g, bpm: g.bpm * factor })
}

/** Moves the "one" along by a beat, for a grid tapped from the wrong beat. */
export function shiftDownbeat() {
  const g = currentGrid()
  setGrid({ ...g, anchorMs: g.anchorMs + 60000 / g.bpm })
}

export function resetGrid() {
  const c = clock()
  if (!c) return
  grids.delete(c.key)
  gridVersion.set((v) => v + 1)
}

// --- Targets ---------------------------------------------------------------

type Target = { el: HTMLElement; drift: boolean }
const targets = new Set<Target>()

/**
 * Lets the engine drive el's variables. With `drift`, el's CSS animations
 * also speed up and slow down with the energy. Returns the detach.
 */
export function attachBeat(el: HTMLElement, opts: { drift?: boolean } = {}) {
  const t = { el, drift: !!opts.drift }
  targets.add(t)
  wake()
  return () => {
    targets.delete(t)
    clear(t)
  }
}

// --- The loop ----------------------------------------------------------------

const reduced = typeof matchMedia === 'function' ? matchMedia('(prefers-reduced-motion: reduce)') : undefined

// Smoothed state, eased every frame so nothing snaps.
let presence = 0
let energy = 0
let swell = 0
let frame = 0
let lastFrame = 0
let lastRate = 1
let lastRateAt = 0
const written = new WeakMap<HTMLElement, Record<string, number>>()

/** One frame of the engine, for canvas scenes that draw rather than style. */
export type BeatFrame = {
  beat: number
  downbeat: number
  swell: number
  energy: number
  /** Four bands, low to high (synthetic until the beat map has real ones). */
  bands: [number, number, number, number]
  /** Fades in when music starts, out when it stops. */
  presence: number
  /** Beats since the grid's anchor, and the beat's length in ms. */
  beatIndex: number
  periodMs: number
  /** Frame time (performance.now) and time since the last frame. */
  now: number
  dt: number
}

const frameListeners = new Set<(f: BeatFrame) => void>()

/** Calls fn every frame the engine runs. Returns the unsubscribe. */
export function onBeatFrame(fn: (f: BeatFrame) => void) {
  frameListeners.add(fn)
  wake()
  return () => {
    frameListeners.delete(fn)
  }
}

function active() {
  return beatSettings.get().level !== 'off' && !reduced?.matches && (targets.size > 0 || frameListeners.size > 0)
}

function wake() {
  if (frame || typeof requestAnimationFrame !== 'function') return
  if (!active()) {
    targets.forEach(clear)
    return
  }
  lastFrame = performance.now()
  frame = requestAnimationFrame(tick)
}

function tick(now: number) {
  frame = 0
  const dt = Math.min(100, now - lastFrame)
  lastFrame = now
  if (!active()) {
    targets.forEach(clear)
    return
  }

  const c = clock()
  const playing = !!c?.playing && document.visibilityState === 'visible'
  // Fades in over about a second when the music starts, out when it stops.
  presence = approach(presence, playing ? 1 : 0, dt, playing ? 900 : 500)
  energy = approach(energy, beatSettings.get().energy, dt, 1500)

  const vars = { beat: 0, downbeat: 0, swell: 0, energy: energy * presence, b0: 0, b1: 0, b2: 0, b3: 0 }
  let beatIndex = 0
  let periodMs = 500
  let breath = 0
  if (c && presence > 0.001) {
    const { bpm, anchorMs } = grids.get(c.key) ?? { bpm: beatSettings.get().defaultBpm, anchorMs: 0 }
    const period = 60000 / bpm
    const since = c.pos - anchorMs
    const n = Math.floor(since / period)
    const into = since - n * period
    const bar = ((n % 4) + 4) % 4
    beatIndex = n
    periodMs = period
    // Rises over the first beat of the bar and sinks over the rest.
    const intoBar = bar * period + into
    breath = intoBar < period ? Math.sin(((intoBar / period) * Math.PI) / 2) : Math.exp(-(intoBar - period) / (period * 1.2))
    // Louder songs hit harder, but even a quiet one keeps a faint pulse.
    const gain = presence * (0.35 + 0.65 * energy)
    vars.beat = gain * pulse(into, Math.min(240, period * 0.4))
    vars.downbeat = bar === 0 ? gain * pulse(into, Math.min(520, period * 0.85)) : 0
    // Synthetic bands: kick on the beat, snare on 2 and 4, offbeat eighths, sixteenths.
    const half = period / 2
    const quarter = period / 4
    vars.b0 = gain * pulse(into, period * 0.5)
    vars.b1 = bar % 2 === 1 ? gain * pulse(into, period * 0.6) : gain * 0.15
    vars.b2 = gain * pulse((into + half) % period, half * 0.7) * 0.8
    vars.b3 = gain * (0.35 + 0.65 * pulse(into % quarter, quarter * 0.6)) * (0.4 + 0.6 * energy)
  }

  swell = approach(swell, breath * presence * (0.45 + 0.55 * energy), dt, 160)
  vars.swell = swell

  // Calm songs drift slowly, loud ones churn. Only now and then: changing
  // an animation's rate is cheap, but not free.
  const rate = 1 + presence * (energy - 0.5) * 1.6
  const setRate = Math.abs(rate - lastRate) > 0.04 && now - lastRateAt > 250
  if (setRate) {
    lastRate = rate
    lastRateAt = now
  }

  const live = presence > 0.001
  if (frameListeners.size) {
    const f: BeatFrame = {
      beat: vars.beat,
      downbeat: vars.downbeat,
      swell: vars.swell,
      energy: vars.energy,
      bands: [vars.b0, vars.b1, vars.b2, vars.b3],
      presence,
      beatIndex,
      periodMs,
      now,
      dt,
    }
    frameListeners.forEach((fn) => fn(f))
  }
  for (const t of targets) {
    write(t.el, vars)
    if (live) t.el.dataset.beatLive = ''
    else delete t.el.dataset.beatLive
    if (t.drift && setRate) for (const a of t.el.getAnimations({ subtree: true })) a.updatePlaybackRate(rate)
  }

  // Idle once everything has settled: nothing playing, nothing fading.
  if (playing || presence > 0.001 || swell > 0.001 || Math.abs(energy - beatSettings.get().energy) > 0.001) frame = requestAnimationFrame(tick)
  else {
    for (const t of targets) if (t.drift) for (const a of t.el.getAnimations({ subtree: true })) a.updatePlaybackRate(1)
    lastRate = 1
  }
}

/** A soft attack (30 ms), then an exponential fade over about `decay` ms. */
function pulse(ms: number, decay: number) {
  if (ms < 0) return 0
  if (ms < 30) return ms / 30
  return Math.exp(-(ms - 30) / (decay / 3))
}

function approach(from: number, to: number, dt: number, ms: number) {
  const next = from + (to - from) * (1 - Math.exp(-dt / (ms / 3)))
  return Math.abs(next - to) < 0.0005 ? to : next
}

function write(el: HTMLElement, vars: Record<string, number>) {
  const prev = written.get(el) ?? {}
  for (const [k, v] of Object.entries(vars)) {
    if (prev[k] !== undefined && Math.abs(prev[k] - v) < 0.003) continue
    prev[k] = v
    el.style.setProperty(`--${k}`, v.toFixed(3))
  }
  written.set(el, prev)
}

function clear(t: Target) {
  for (const k of ['beat', 'downbeat', 'swell', 'energy', 'b0', 'b1', 'b2', 'b3']) t.el.style.removeProperty(`--${k}`)
  delete t.el.dataset.beatLive
  written.delete(t.el)
  if (t.drift) for (const a of t.el.getAnimations({ subtree: true })) a.updatePlaybackRate(1)
}

player.subscribe(wake)
beatSettings.subscribe(wake)
reduced?.addEventListener('change', wake)
if (typeof document !== 'undefined') document.addEventListener('visibilitychange', wake)
