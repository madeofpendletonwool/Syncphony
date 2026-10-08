import type { BeatFrame } from './beat'

// Scene painters: canvas drawings of the music in the art's colors, for
// the app's backdrop (components/shell/backdrop-scene.tsx) and the big
// visualizer (components/visualizer). Each is a factory, so every canvas
// gets its own state.

export type Colors = { vibrant: string; dominant: string; muted: string; light: string }

export type Painter = (ctx: CanvasRenderingContext2D, w: number, h: number, f: BeatFrame, c: Colors) => void

/** The palette as the element sees it (it eases between songs). */
export function readColors(el: HTMLElement): Colors {
  const s = getComputedStyle(el)
  const v = (name: string) => s.getPropertyValue(name).trim() || 'white'
  return { vibrant: v('--pal-vibrant'), dominant: v('--pal-dominant'), muted: v('--pal-muted'), light: v('--pal-light') }
}

/** Eases a value: fast up (attack), slower down (release). */
export function follow(from: number, to: number, dt: number, attack: number, release: number) {
  const ms = to > from ? attack : release
  return from + (to - from) * (1 - Math.exp(-dt / (ms / 3)))
}

/** A background equalizer: soft columns along the bottom, the song's spectrum mirrored, bass in the middle. */
function horizon(): Painter {
  const N = 32
  const level = new Float32Array(N)
  return (ctx, w, h, f, c) => {
    const t = f.now / 1000
    const gap = w / N
    const bands = f.spectrum.length
    const grad = ctx.createLinearGradient(0, h, 0, h * 0.35)
    grad.addColorStop(0, c.dominant)
    grad.addColorStop(0.6, c.vibrant)
    grad.addColorStop(1, c.light)
    ctx.fillStyle = grad
    for (let i = 0; i < N; i++) {
      const x = (i + 0.5) / N
      const d = Math.abs(x - 0.5) * 2 // 0 middle, 1 edges
      // Between the two nearest bands.
      const b = d * (bands - 1)
      const k = Math.min(Math.floor(b), bands - 2)
      const shape = f.spectrum[k] * (1 - (b - k)) + f.spectrum[k + 1] * (b - k)
      // Neighbouring columns share a band; a little wander tells them apart.
      const wander = f.energy * 0.12 * (0.5 + 0.5 * Math.sin(t * (1.3 + (i % 7) * 0.31) + i * 1.7))
      level[i] = follow(level[i], Math.min(1, shape + wander), f.dt, 50, 320)
      const bh = h * (0.04 + level[i] * 0.5)
      const bw = gap * 0.62
      roundRect(ctx, i * gap + (gap - bw) / 2, h - bh, bw, bh + bw, bw / 2)
    }
  }
}

/** One slow ring a bar, spreading from behind the art like a drop in still water. */
function ripples(): Painter {
  type Ring = { born: number; color: keyof Colors }
  let rings: Ring[] = []
  let last = Number.NaN
  const palette: (keyof Colors)[] = ['vibrant', 'light']
  return (ctx, w, h, f, c) => {
    const bar = Math.floor(f.beatIndex / 4)
    if (bar !== last) {
      if (!Number.isNaN(last)) rings.push({ born: f.now, color: palette[Math.abs(bar) % palette.length] })
      last = bar
    }
    const life = 5200
    rings = rings.filter((r) => f.now - r.born < life)
    const cx = w / 2
    const cy = h * 0.3
    const reach = Math.hypot(w, h) * 0.7
    for (const r of rings) {
      const k = (f.now - r.born) / life
      const ease = 1 - (1 - k) ** 2.5
      ctx.globalAlpha = f.presence * (1 - k) ** 2 * (0.35 + 0.35 * f.energy)
      ctx.strokeStyle = c[r.color]
      ctx.lineWidth = h * 0.02 * (1 - k * 0.5)
      ctx.beginPath()
      ctx.arc(cx, cy, h * 0.05 + ease * reach, 0, Math.PI * 2)
      ctx.stroke()
    }
  }
}

/**
 * Curtains of light hanging from the top of the screen, like the real
 * aurora: soft vertical rays whose hems wave slowly. They reach further
 * down on the bar and ripple faster when the song is loud.
 */
function aurora(): Painter {
  let phase = 0
  const curtains: { top: number; len: number; color: keyof Colors; f: number; o: number }[] = [
    // Back to front: a tall faint veil, the main curtain, then lower,
    // shorter folds in the other colors, so it has depth.
    { top: -0.02, len: 0.5, color: 'muted', f: 0.6, o: 5.1 },
    { top: 0.05, len: 0.38, color: 'vibrant', f: 1, o: 0 },
    { top: 0.2, len: 0.28, color: 'light', f: 1.6, o: 2.4 },
    { top: 0.34, len: 0.22, color: 'dominant', f: 2.1, o: 3.7 },
  ]
  const COLS = 40
  return (ctx, w, h, f, c) => {
    phase += (f.dt / 1000) * (0.12 + f.energy * 0.35)
    const colW = w / COLS
    for (const k of curtains) {
      for (let i = 0; i <= COLS; i++) {
        const x = i / COLS
        // The hem waves; the rays' brightness shimmers along it.
        const wave = Math.sin(x * 4 * k.f + phase + k.o) * 0.5 + Math.sin(x * 9 * k.f - phase * 1.7 + k.o) * 0.25
        const shimmer = 0.55 + 0.45 * Math.sin(x * 23 + phase * 2.3 + k.o) ** 2
        const top = (k.top + wave * 0.05) * h
        const len = h * k.len * (0.75 + 0.2 * f.energy + 0.3 * f.swell) * (0.8 + 0.2 * shimmer)
        const g = ctx.createLinearGradient(0, top, 0, top + len)
        g.addColorStop(0, 'transparent')
        g.addColorStop(0.55, c[k.color])
        g.addColorStop(1, 'transparent')
        ctx.globalAlpha = f.presence * shimmer * (0.3 + 0.25 * f.swell + 0.15 * f.energy)
        ctx.fillStyle = g
        ctx.fillRect(i * colW - colW, top, colW * 2.2, len)
      }
    }
  }
}

/**
 * The palette mesh, alive: big soft pools of the art's colors that wander,
 * swell and brighten once a bar, and wander faster when the song is loud.
 */
function mesh(): Painter {
  let phase = Math.random() * 100
  const pools: { color: keyof Colors; x: number; y: number; ax: number; ay: number; fx: number; fy: number; r: number }[] = [
    { color: 'vibrant', x: 0.3, y: 0.25, ax: 0.18, ay: 0.12, fx: 0.7, fy: 0.9, r: 0.55 },
    { color: 'light', x: 0.72, y: 0.45, ax: 0.15, ay: 0.18, fx: 0.5, fy: 0.6, r: 0.45 },
    { color: 'dominant', x: 0.45, y: 0.8, ax: 0.22, ay: 0.1, fx: 0.4, fy: 0.8, r: 0.6 },
  ]
  return (ctx, w, h, f, c) => {
    phase += (f.dt / 1000) * (0.08 + f.energy * 0.3)
    const m = Math.max(w, h)
    for (const p of pools) {
      const x = (p.x + p.ax * Math.sin(phase * p.fx)) * w
      const y = (p.y + p.ay * Math.cos(phase * p.fy)) * h
      const r = m * p.r * (1 + 0.22 * f.swell)
      const g = ctx.createRadialGradient(x, y, 0, x, y, r)
      g.addColorStop(0, c[p.color])
      g.addColorStop(1, 'transparent')
      ctx.globalAlpha = f.presence * (0.28 + 0.4 * f.swell + 0.12 * f.energy)
      ctx.fillStyle = g
      ctx.fillRect(0, 0, w, h)
    }
  }
}

/** Embers rising through the glass; they glow on the beat and quicken with energy. */
function embers(): Painter {
  const N = 70
  const roles: (keyof Colors)[] = ['vibrant', 'light', 'vibrant', 'muted']
  const ps = Array.from({ length: N }, () => ({
    x: Math.random(),
    y: Math.random(),
    r: 0.8 + Math.random() * 1.8,
    s: 0.4 + Math.random() * 0.8,
    w: Math.random() * Math.PI * 2,
    hit: Math.random(),
    color: roles[Math.floor(Math.random() * roles.length)],
  }))
  return (ctx, w, h, f, c) => {
    const rise = (f.dt / 1000) * (0.02 + f.energy * 0.09)
    for (const p of ps) {
      p.y -= rise * p.s * (1 + f.swell * 0.8)
      p.w += f.dt / 1400
      if (p.y < -0.05) {
        p.y = 1.05
        p.x = Math.random()
      }
      const x = (p.x + Math.sin(p.w) * 0.02) * w
      const y = p.y * h
      // Some embers flare on the beat, the rest just drift.
      const flare = p.hit > 0.55 ? f.beat * (p.hit - 0.55) * 2.2 : 0
      ctx.globalAlpha = f.presence * Math.min(1, 0.35 + flare + f.energy * 0.2)
      ctx.fillStyle = c[p.color]
      ctx.beginPath()
      ctx.arc(x, y, p.r * (1 + flare * 0.8), 0, Math.PI * 2)
      ctx.fill()
    }
  }
}

/**
 * The song's spectrum as a ring of bars around the middle of the screen
 * (the album art sits inside it), mirrored left and right, bass at the
 * top. The ring swells on the bar and the bars flare on the beat.
 * For the big visualizer: drawn at full resolution.
 */
function spectrum(): Painter {
  const N = 72
  const level = new Float32Array(N)
  let spin = 0
  return (ctx, w, h, f, c) => {
    const cx = w / 2
    const cy = h / 2
    const m = Math.min(w, h)
    const inner = m * (0.2 + 0.012 * f.swell)
    const reach = m * 0.2
    const bands = f.spectrum.length
    spin += (f.dt / 1000) * (0.03 + f.energy * 0.08)
    // A soft glow behind the ring.
    const glow = ctx.createRadialGradient(cx, cy, inner * 0.8, cx, cy, inner + reach * 1.4)
    glow.addColorStop(0, c.vibrant)
    glow.addColorStop(1, 'transparent')
    ctx.globalAlpha = f.presence * (0.18 + 0.25 * f.swell)
    ctx.fillStyle = glow
    ctx.fillRect(0, 0, w, h)
    ctx.globalAlpha = f.presence
    ctx.lineCap = 'round'
    const width = ((Math.PI * 2 * inner) / N) * 0.55
    for (let i = 0; i < N; i++) {
      // Mirrored: 0 at the top through the band range down each side.
      const half = i < N / 2 ? i / (N / 2) : (N - i) / (N / 2)
      const b = half * (bands - 1)
      const k = Math.min(Math.floor(b), bands - 2)
      const v = f.spectrum[k] * (1 - (b - k)) + f.spectrum[k + 1] * (b - k)
      level[i] = follow(level[i], v, f.dt, 40, 260)
      const len = reach * (0.06 + level[i] * 0.94) * (1 + f.beat * 0.15)
      const a = (i / N) * Math.PI * 2 - Math.PI / 2 + spin
      const cos = Math.cos(a)
      const sin = Math.sin(a)
      ctx.strokeStyle = level[i] > 0.75 ? c.light : c.vibrant
      ctx.lineWidth = width
      ctx.beginPath()
      ctx.moveTo(cx + cos * inner, cy + sin * inner)
      ctx.lineTo(cx + cos * (inner + len), cy + sin * (inner + len))
      ctx.stroke()
    }
  }
}

/** The spectrum's value at x (0–1 across the bands), between the two nearest. */
function bandAt(spec: ArrayLike<number>, x: number) {
  const b = Math.max(0, Math.min(1, x)) * (spec.length - 1)
  const k = Math.min(Math.floor(b), spec.length - 2)
  return spec[k] * (1 - (b - k)) + spec[k + 1] * (b - k)
}

/**
 * A tunnel of the song's own spectrum: every few hundredths of a second
 * a ring is cast in the shape of the spectrum at that moment and flies at
 * you, so the tunnel walls are the song's recent past. Bars' first beats
 * cast a bright ring; the whole thing turns faster when the song is loud.
 */
function tunnel(): Painter {
  type Ring = { z: number; shape: Float32Array; bright: boolean; hue: number }
  const POINTS = 96
  let rings: Ring[] = []
  let castAt = 0
  let lastBar = Number.NaN
  let spin = 0
  let wander = 0
  return (ctx, w, h, f, c) => {
    const travel = (f.dt / 1000) * (0.32 + f.energy * 0.55 + f.swell * 0.2)
    for (const r of rings) r.z -= travel
    rings = rings.filter((r) => r.z > 0.02)
    const bar = Math.floor(f.beatIndex / 4)
    const downbeat = f.downbeat > 0.5 && bar !== lastBar
    if (downbeat) lastBar = bar
    if (f.now - castAt > 30 || downbeat) {
      castAt = f.now
      const shape = new Float32Array(POINTS)
      for (let i = 0; i < POINTS; i++) {
        // Mirrored around the ring: bass on two opposite sides, treble between.
        const a = i / POINTS
        shape[i] = bandAt(f.spectrum, 1 - Math.abs(Math.cos(a * Math.PI * 2)))
      }
      rings.push({ z: 1, shape, bright: downbeat, hue: rings.length })
    }
    spin += (f.dt / 1000) * (0.05 + f.energy * 0.25) + f.beat * 0.002
    wander += f.dt / 1000
    const m = Math.min(w, h)
    const cx = w / 2 + Math.sin(wander * 0.21) * m * 0.06
    const cy = h / 2 + Math.cos(wander * 0.17) * m * 0.05
    ctx.globalCompositeOperation = 'lighter'
    ctx.lineJoin = 'round'
    // Far to near, so the near rings draw over.
    for (let j = 0; j < rings.length; j++) {
      const r = rings[j]
      const radius = (m * 0.045) / r.z
      if (radius > Math.hypot(w, h)) continue
      const near = 1 - r.z
      ctx.globalAlpha = f.presence * Math.min(1, near * 1.8) * (r.bright ? 1 : 0.32) * Math.min(1, r.z * 5)
      ctx.strokeStyle = r.bright ? c.light : near > 0.45 ? c.vibrant : c.dominant
      ctx.lineWidth = Math.max(0.75, (r.bright ? 3 : 1) * near * near * 3.5)
      ctx.beginPath()
      for (let i = 0; i <= POINTS; i++) {
        const a = (i / POINTS) * Math.PI * 2 + spin + r.z * 0.8
        const k = 1 + r.shape[i % POINTS] * 0.3
        const x = cx + Math.cos(a) * radius * k
        const y = cy + Math.sin(a) * radius * k
        if (i === 0) ctx.moveTo(x, y)
        else ctx.lineTo(x, y)
      }
      ctx.stroke()
    }
    ctx.globalCompositeOperation = 'source-over'
  }
}

/**
 * Rain on dark water: ripples open all over the screen, a few on every
 * beat and a burst of them on each bar, with the treble flicking tiny
 * ones in between.
 */
function rain(): Painter {
  type Drop = { x: number; y: number; born: number; size: number; life: number; color: keyof Colors }
  let drops: Drop[] = []
  let lastBeat = Number.NaN
  let flick = 0
  const roles: (keyof Colors)[] = ['vibrant', 'light', 'vibrant', 'muted']
  const drop = (f: BeatFrame, size: number, life: number, near?: { x: number; y: number }) => {
    drops.push({
      x: near ? near.x + (Math.random() - 0.5) * 0.2 : Math.random(),
      y: near ? near.y + (Math.random() - 0.5) * 0.2 : Math.random(),
      born: f.now,
      size: size * (0.7 + Math.random() * 0.6),
      life: life * (0.8 + Math.random() * 0.4),
      color: roles[Math.floor(Math.random() * roles.length)],
    })
  }
  return (ctx, w, h, f, c) => {
    if (f.beatIndex !== lastBeat) {
      lastBeat = f.beatIndex
      const one = ((f.beatIndex % 4) + 4) % 4 === 0
      const count = Math.round((one ? 6 : 2) + f.energy * (one ? 6 : 3))
      const at = { x: 0.2 + Math.random() * 0.6, y: 0.2 + Math.random() * 0.6 }
      if (one) drop(f, 0.55, 3600, at)
      for (let i = 0; i < count; i++) drop(f, one ? 0.22 : 0.16, 2400, one ? at : undefined)
    }
    // The treble rains between the beats.
    const treble = f.bands[3]
    flick += (f.dt / 1000) * treble * treble * 14
    while (flick > 1) {
      flick -= 1
      drop(f, 0.06, 1400)
    }
    drops = drops.filter((d) => f.now - d.born < d.life).slice(-140)
    const m = Math.min(w, h)
    ctx.globalCompositeOperation = 'lighter'
    for (const d of drops) {
      const k = (f.now - d.born) / d.life
      const ease = 1 - (1 - k) ** 2.2
      const r = m * d.size * ease
      ctx.strokeStyle = c[d.color]
      // A ring and a fainter one inside it, like water.
      for (const [scale, alpha] of [
        [1, 0.85],
        [0.62, 0.4],
      ] as const) {
        ctx.globalAlpha = f.presence * (1 - k) ** 1.6 * alpha
        ctx.lineWidth = Math.max(1, m * 0.006 * (1 - k))
        ctx.beginPath()
        ctx.ellipse(d.x * w, d.y * h, r * scale, r * scale * 0.92, 0, 0, Math.PI * 2)
        ctx.stroke()
      }
    }
    ctx.globalCompositeOperation = 'source-over'
  }
}

/**
 * Sparks: hundreds of glowing embers swirling up, quicker with the
 * energy, flaring on the beat, and a fountain of them from below on each
 * bar.
 */
function sparks(): Painter {
  type Spark = { x: number; y: number; vx: number; vy: number; size: number; heat: number; color: keyof Colors }
  const roles: (keyof Colors)[] = ['vibrant', 'light', 'vibrant', 'dominant']
  const make = (burst: boolean): Spark => ({
    x: burst ? 0.5 + (Math.random() - 0.5) * 0.35 : Math.random(),
    y: burst ? 1.02 : Math.random() * 1.1,
    vx: burst ? (Math.random() - 0.5) * 0.9 : 0,
    vy: burst ? -(0.6 + Math.random() * 0.7) : 0,
    size: 0.6 + Math.random() * 1.4,
    heat: burst ? 1 : Math.random(),
    color: roles[Math.floor(Math.random() * roles.length)],
  })
  let sparks = Array.from({ length: 320 }, () => make(false))
  let lastBar = Number.NaN
  let t = 0
  // A soft glow per color, drawn once and stamped for every spark.
  const sprites = new Map<string, HTMLCanvasElement>()
  const sprite = (color: string) => {
    let s = sprites.get(color)
    if (!s) {
      s = document.createElement('canvas')
      s.width = s.height = 64
      const g = s.getContext('2d')!
      const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32)
      grad.addColorStop(0, color)
      grad.addColorStop(0.25, color)
      grad.addColorStop(1, 'transparent')
      g.fillStyle = grad
      g.fillRect(0, 0, 64, 64)
      if (sprites.size > 24) sprites.clear()
      sprites.set(color, s)
    }
    return s
  }
  return (ctx, w, h, f, c) => {
    const dt = f.dt / 1000
    t += dt
    const bar = Math.floor(f.beatIndex / 4)
    if (f.downbeat > 0.5 && bar !== lastBar) {
      lastBar = bar
      sparks.push(...Array.from({ length: Math.round(30 + f.energy * 40) }, () => make(true)))
    }
    const lift = 0.05 + f.energy * 0.22 + f.swell * 0.08
    const m = Math.min(w, h)
    ctx.globalCompositeOperation = 'lighter'
    for (const p of sparks) {
      // Rising, swirling in a slow turbulence; fountain sparks arc and slow.
      p.vx += Math.sin(p.y * 7 + t * 0.9) * dt * 0.08
      p.vx *= 1 - dt * 0.8
      p.vy += dt * 0.45 * (p.vy < -lift ? 1 : 0)
      p.x += p.vx * dt
      p.y += (Math.min(p.vy, 0) - lift * (0.5 + p.size * 0.4)) * dt
      p.heat = Math.max(0.15, p.heat - dt * 0.25)
      if (p.y < -0.05 || p.x < -0.05 || p.x > 1.05) Object.assign(p, make(false), { y: 1.05 })
      const flare = f.beat * (p.size > 1.4 ? 0.9 : 0.35)
      const r = m * 0.012 * p.size * (1 + flare + f.bands[0] * 0.5)
      ctx.globalAlpha = f.presence * Math.min(1, 0.25 + p.heat * 0.6 + flare)
      ctx.drawImage(sprite(c[p.color]), p.x * w - r * 2, p.y * h - r * 2, r * 4, r * 4)
    }
    // Fountains thin out to the usual crowd.
    if (sparks.length > 320) sparks = sparks.filter((p, i) => i < 320 || p.heat > 0.2)
    ctx.globalCompositeOperation = 'source-over'
  }
}

export const painters = { mesh, horizon, ripples, aurora, embers }

/** The visualizer's own canvas shows, drawn sharp at full size. */
export const showPainters = { spectrum, tunnel, rain, sparks }

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath()
  ctx.roundRect(x, y, w, h, r)
  ctx.fill()
}
