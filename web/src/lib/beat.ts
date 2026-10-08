// The beat engine (MAD-775). One rAF loop turns the room's playback
// position into a few CSS custom properties, so the app can breathe with
// the song without re-rendering anything.
//
// Where the beat comes from, first match wins:
//   1. a grid tapped in the beat lab, for a song the analysis got wrong;
//   2. the song's beat map from the server (lib/beat-map.ts);
//   3. the default tempo, while the map loads or for songs without one:
//      in time, but not in phase.
// The map also gives the energy (its sections and loudness) and the
// spectrum.
//
// Variables, written only to the elements that use them (a var on :root
// would restyle the whole tree every frame):
//   --beat      0→1 on each beat, easing back down. Scaled by energy.
//   --downbeat  the same on the first beat of each bar, slower to fade.
//   --swell     a slow breath once a bar: rises over a beat, fades over the
//               rest. For big surfaces, where a per-beat flash reads as strobe.
//   --energy    how hard the song is going right now, slowly smoothed.
//   --b0..--b3  bass, low mids, high mids and treble, for the equalizer.
// Elements also get data-beat-live while the engine drives them, so CSS
// can swap a canned animation for the live one.

import { beatAt, loudnessAt, sectionEnergy, spectrumAt, type BeatMap } from './beat-map'
import { positionAt, type NowPlaying } from './now-playing'
import { createStore } from './store'

export type BeatLevel = 'off' | 'subtle'

/** The effects the beat lab can tune one by one. */
export const EFFECTS = {
  breath: 'Backdrop breathing',
  scene: 'Backdrop scene',
  drift: 'Backdrop drift',
  art: 'Album art lift',
  glass: 'Glass shine',
  eq: 'Equalizer',
  lyrics: 'Lyrics glow',
  waveform: 'Waveform scrubber',
} as const
export type Effect = keyof typeof EFFECTS

export type BeatSettings = {
  level: BeatLevel
  /** How strongly everything moves, 0.5 (calmer) – 1.75 (stronger). */
  intensity: number
  /** The backdrop scene, or 'auto' to pick one per song. */
  scene: string
  /** The full-screen visualizer's show, or 'auto'. */
  show: string
  /** Each effect's strength, 0 (off) – 2. */
  effects: Record<Effect, number>
  /** How late this device's sound is, ms: a Bluetooth speaker, say. Visuals wait for it. */
  delayMs: number
}

const SETTINGS_KEY = 'syncphony-visuals'
export const BEAT_DEFAULTS: BeatSettings = {
  level: 'subtle',
  intensity: 1,
  scene: 'auto',
  show: 'auto',
  effects: { breath: 1, scene: 1, drift: 1, art: 1, glass: 1, eq: 1, lyrics: 1, waveform: 1 },
  delayMs: 0,
}

export const beatSettings = createStore<BeatSettings>(readSettings())
beatSettings.subscribe(() => {
  const s = beatSettings.get()
  try {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(s))
  } catch {
    // Private mode: settings last until the tab closes.
  }
  applyEffects(s)
})
applyEffects(beatSettings.get())

/** Changes some settings; effects merge into the current ones. */
export function setBeatSettings(next: Partial<Omit<BeatSettings, 'effects'>> & { effects?: Partial<Record<Effect, number>> }) {
  beatSettings.set((s) => ({ ...s, ...next, effects: { ...s.effects, ...next.effects } }))
}

function readSettings(): BeatSettings {
  const clamp = (v: unknown, lo: number, hi: number, d: number) => (typeof v === 'number' && Number.isFinite(v) ? Math.min(hi, Math.max(lo, v)) : d)
  try {
    const s = JSON.parse(localStorage.getItem(SETTINGS_KEY) ?? '{}') as Partial<BeatSettings>
    const effects = { ...BEAT_DEFAULTS.effects }
    for (const k of Object.keys(effects) as Effect[]) effects[k] = clamp(s.effects?.[k], 0, 2, effects[k])
    return {
      level: s.level === 'off' ? 'off' : 'subtle',
      intensity: clamp(s.intensity, 0.5, 1.75, BEAT_DEFAULTS.intensity),
      scene: typeof s.scene === 'string' ? s.scene : BEAT_DEFAULTS.scene,
      show: typeof s.show === 'string' ? s.show : BEAT_DEFAULTS.show,
      effects,
      delayMs: clamp(s.delayMs, -300, 600, 0),
    }
  } catch {
    return BEAT_DEFAULTS
  }
}

/**
 * The CSS effects read their strength from --fx-* on the root: set once
 * when the settings change, never per frame.
 */
function applyEffects(s: BeatSettings) {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  for (const [k, v] of Object.entries(s.effects)) root.style.setProperty(`--fx-${k}`, String(v))
  root.toggleAttribute('data-fx-eq-off', s.effects.eq === 0)
}

/** Whether the full-screen visualizer is open in the app. */
export const visualizerOpen = createStore(false)

/** Tempo for songs with no beat map yet. */
const DEFAULT_BPM = 120

/**
 * What's playing, and its beat map once loaded. Set by useBeatSync, which
 * the app shell and the big screen each mount once.
 */
export const beatSource = createStore<{ np: NowPlaying | null; map: BeatMap | null; mapKey: string | null }>({
  np: null,
  map: null,
  mapKey: null,
})

/** A song's beat grid: its tempo and the position of one downbeat, in ms. */
export type Grid = { bpm: number; anchorMs: number }

/** Grids tapped in the beat lab, by song. They beat the song's map. */
const tapped = new Map<string, Grid>()
export const gridVersion = createStore(0)

function keyOf(np: NowPlaying) {
  return np.itemId ?? np.track.trackId
}

/** Where the music is now, and which song's grid applies. */
function clock(): { key: string; pos: number; playing: boolean } | null {
  const { np } = beatSource.get()
  if (np && !np.paused) return { key: keyOf(np), pos: positionAt(np, Date.now()) - beatSettings.get().delayMs, playing: true }
  if (np) return { key: keyOf(np), pos: np.positionMs, playing: false }
  return null
}

/** The current song's map, if it's loaded. */
function mapFor(key: string) {
  const { map, mapKey } = beatSource.get()
  return map && mapKey === key ? map : null
}

/** Where the current song's beat comes from, for the beat lab. */
export type BeatOrigin = 'tapped' | 'analysed' | 'steady' | 'default'

export function currentBeat(): Grid & { origin: BeatOrigin } {
  const c = clock()
  const own = c && tapped.get(c.key)
  if (own) return { ...own, origin: 'tapped' }
  const map = c && mapFor(c.key)
  if (map && map.bpm > 0) return { bpm: map.bpm, anchorMs: map.beats[map.downbeat] ?? 0, origin: 'analysed' }
  if (map) return { bpm: 0, anchorMs: 0, origin: 'steady' }
  return { bpm: DEFAULT_BPM, anchorMs: 0, origin: 'default' }
}

function setGrid(g: Grid) {
  const c = clock()
  if (!c) return
  tapped.set(c.key, { bpm: Math.min(220, Math.max(50, g.bpm)), anchorMs: g.anchorMs })
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

/** The current beat as a grid to adjust: tapped, analysed, or the default. */
function adjustable(): Grid {
  const b = currentBeat()
  return b.bpm > 0 ? b : { bpm: DEFAULT_BPM, anchorMs: 0 }
}

export function nudge(ms: number) {
  const g = adjustable()
  setGrid({ ...g, anchorMs: g.anchorMs + ms })
}

export function scaleTempo(factor: number) {
  const g = adjustable()
  setGrid({ ...g, bpm: g.bpm * factor })
}

/** Moves the "one" along by a beat, for a grid that starts its bars in the wrong place. */
export function shiftDownbeat() {
  const g = adjustable()
  setGrid({ ...g, anchorMs: g.anchorMs + 60000 / g.bpm })
}

/** Forgets the song's tapped grid, back to its beat map. */
export function resetGrid() {
  const c = clock()
  if (!c) return
  tapped.delete(c.key)
  taps = []
  gridVersion.set((v) => v + 1)
}

// --- Delay calibration -------------------------------------------------------

let heard: number[] = []

/**
 * A tap on the beat as it's heard here, to work out how late this
 * device's sound is (a Bluetooth speaker can add a fifth of a second).
 * Compares the taps with the song's beat map; after four, sets delayMs to
 * their typical lag. Returns how many taps so far, or null if the song
 * playing has no beat to compare with.
 */
export function calibrateTap(): number | null {
  const { np } = beatSource.get()
  const map = np && !np.paused ? mapFor(keyOf(np)) : null
  if (!np || !map || map.bpm <= 0) return null
  const pos = positionAt(np, Date.now())
  const last = heard.at(-1)
  if (last === undefined || pos - last > 2000 || pos < last) heard = []
  heard.push(pos)
  heard = heard.slice(-16)
  const lags = heard
    .map((t) => beatAt(map, t))
    .filter((p) => p !== null)
    .map((p) => (p.into > p.period / 2 ? p.into - p.period : p.into))
    .sort((a, b) => a - b)
  if (lags.length >= 4) {
    const median = lags[Math.floor(lags.length / 2)]
    setBeatSettings({ delayMs: Math.round(Math.min(600, Math.max(-300, median))) })
  }
  return heard.length
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
let loud = 0
let swell = 0
let frame = 0
let lastFrame = 0
let lastRate = 1
let lastRateAt = 0
const written = new WeakMap<HTMLElement, Record<string, number>>()
const BANDS = 12
const spectrum = new Float32Array(BANDS)
const level = new Float32Array(BANDS)

/** One frame of the engine, for canvas scenes that draw rather than style. */
export type BeatFrame = {
  beat: number
  downbeat: number
  swell: number
  energy: number
  /** Four bands, low to high. */
  bands: [number, number, number, number]
  /** Twelve bands, low to high, 0–1 (from the beat map, else made up from the beat). */
  spectrum: Float32Array
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

/** Where pos falls in the song's beat, from whichever source applies. */
function place(key: string, pos: number): { index: number; into: number; period: number; bar: number } | null {
  const own = tapped.get(key)
  const map = mapFor(key)
  if (!own && map) return map.bpm > 0 ? beatAt(map, pos) : null
  const { bpm, anchorMs } = own ?? { bpm: DEFAULT_BPM, anchorMs: 0 }
  const period = 60000 / bpm
  const since = pos - anchorMs
  const n = Math.floor(since / period)
  return { index: n, into: since - n * period, period, bar: ((n % 4) + 4) % 4 }
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
  const map = c ? mapFor(c.key) : null
  const intensity = beatSettings.get().intensity
  // Fades in over about a second when the music starts, out when it stops.
  presence = approach(presence, playing ? 1 : 0, dt, playing ? 900 : 500)

  // Energy: the section's, nudged by how loud it is right now.
  let target = 0.5
  if (map && c) {
    loud = approach(loud, loudnessAt(map, c.pos), dt, 600)
    target = 0.55 * sectionEnergy(map, c.pos) + 0.45 * loud
  }
  energy = approach(energy, target, dt, 1500)

  const vars = { beat: 0, downbeat: 0, swell: 0, energy: energy * presence, b0: 0, b1: 0, b2: 0, b3: 0 }
  let beatIndex = 0
  let periodMs = 500
  let breath = 0
  const p = c && presence > 0.001 ? place(c.key, c.pos) : null
  // Louder songs hit harder, but even a quiet one keeps a faint pulse.
  const gain = presence * (0.35 + 0.65 * energy) * intensity
  if (p) {
    beatIndex = p.index
    periodMs = p.period
    // Never more than three flashes a second: at very fast tempos, every other beat.
    const flash = p.period >= 333 || p.index % 2 === 0
    vars.beat = flash ? gain * pulse(p.into, Math.min(240, p.period * 0.4)) : 0
    vars.downbeat = p.bar === 0 ? gain * pulse(p.into, Math.min(520, p.period * 0.85)) : 0
    // Rises over the first beat of the bar and sinks over the rest.
    const intoBar = p.bar * p.period + p.into
    breath =
      intoBar < p.period ? Math.sin(((intoBar / p.period) * Math.PI) / 2) : Math.exp(-(intoBar - p.period) / (p.period * 1.2))
  }

  if (map && c && presence > 0.001) {
    spectrumAt(map, c.pos, spectrum)
  } else if (p) {
    // No map: make a spectrum up from the beat, kick low and hats high.
    const kick = pulse(p.into, p.period * 0.5)
    const snare = p.bar % 2 === 1 ? pulse(p.into, p.period * 0.6) : 0.15
    const hats = 0.35 + 0.65 * pulse(p.into % (p.period / 4), p.period * 0.15)
    for (let k = 0; k < BANDS; k++) {
      const x = k / (BANDS - 1)
      spectrum[k] = kick * Math.max(0, 1 - x * 3) + snare * Math.exp(-(((x - 0.45) / 0.2) ** 2)) + hats * x ** 2 * 0.7
    }
  } else {
    spectrum.fill(0)
  }
  // Each band's level: up fast, down slower, and only the top of its range,
  // so the bars move rather than sit high.
  for (let k = 0; k < BANDS; k++) {
    const v = Math.max(0, (spectrum[k] - 0.4) / 0.6) ** 1.3
    level[k] = follow(level[k], v * presence, dt, 40, 220)
  }
  const group = (from: number) => ((level[from] + level[from + 1] + level[from + 2]) / 3) * Math.min(1.5, intensity)
  vars.b0 = group(0)
  vars.b1 = group(3)
  vars.b2 = group(6)
  vars.b3 = group(9)

  swell = approach(swell, breath * presence * (0.45 + 0.55 * energy) * intensity, dt, 160)
  vars.swell = swell

  // Calm songs drift slowly, loud ones churn. Only now and then: changing
  // an animation's rate is cheap, but not free.
  const rate = 1 + presence * (energy - 0.5) * 1.6 * beatSettings.get().effects.drift
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
      spectrum: level,
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
  if (playing || presence > 0.001 || swell > 0.001 || Math.abs(energy - target) > 0.001) frame = requestAnimationFrame(tick)
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

/** Eases a value: fast up (attack), slower down (release). */
function follow(from: number, to: number, dt: number, attack: number, release: number) {
  return from + (to - from) * (1 - Math.exp(-dt / ((to > from ? attack : release) / 3)))
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

beatSource.subscribe(wake)
beatSettings.subscribe(wake)
reduced?.addEventListener('change', wake)
if (typeof document !== 'undefined') document.addEventListener('visibilitychange', wake)
