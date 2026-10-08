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

/**
 * Flying through stars in the art's colors: faster when the song is loud,
 * a surge on each bar, streaks that lengthen with the speed.
 */
function starfield(): Painter {
  const N = 260
  const roles: (keyof Colors)[] = ['light', 'vibrant', 'light', 'muted']
  const stars = Array.from({ length: N }, () => ({
    x: Math.random() * 2 - 1,
    y: Math.random() * 2 - 1,
    z: Math.random(),
    color: roles[Math.floor(Math.random() * roles.length)],
  }))
  let surge = 0
  return (ctx, w, h, f, c) => {
    surge = Math.max(surge * Math.exp(-f.dt / 350), f.downbeat)
    const speed = (f.dt / 1000) * (0.08 + f.energy * 0.35 + surge * 0.5)
    const cx = w / 2
    const cy = h / 2
    const scale = Math.max(w, h) * 0.5
    ctx.lineCap = 'round'
    for (const s of stars) {
      const pz = s.z
      s.z -= speed
      if (s.z <= 0.02) {
        s.x = Math.random() * 2 - 1
        s.y = Math.random() * 2 - 1
        s.z = 1
        continue
      }
      const x = cx + (s.x / s.z) * scale * 0.3
      const y = cy + (s.y / s.z) * scale * 0.3
      const px = cx + (s.x / pz) * scale * 0.3
      const py = cy + (s.y / pz) * scale * 0.3
      if (x < -50 || x > w + 50 || y < -50 || y > h + 50) {
        s.z = 0
        continue
      }
      const near = 1 - s.z
      ctx.globalAlpha = f.presence * Math.min(1, near * 1.4) * (0.6 + 0.4 * f.beat)
      ctx.strokeStyle = c[s.color]
      ctx.lineWidth = Math.max(1, near * near * 5)
      ctx.beginPath()
      ctx.moveTo(px, py)
      ctx.lineTo(x, y)
      ctx.stroke()
    }
  }
}

export const painters = { mesh, horizon, ripples, aurora, embers }

/** The visualizer's own canvas shows, drawn sharp at full size. */
export const showPainters = { spectrum, starfield }

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath()
  ctx.roundRect(x, y, w, h, r)
  ctx.fill()
}
