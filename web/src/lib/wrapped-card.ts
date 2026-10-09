import { formatListening } from './history'
import type { Recap } from './playlists'
import { queueArtworkUrl } from './playback'

// The night's Wrapped as one image, story-sized, to post or send. Drawn
// on a canvas; the artwork comes from the server, so it doesn't taint it.

const W = 1080
const H = 1920
const PAD = 96

export type CardInput = {
  recap: Recap
  roomId: string
  roomName: string
  title: string
  date: string
  nameOf: (userId: string) => string
}

function loadImage(src: string | undefined): Promise<HTMLImageElement | null> {
  if (!src) return Promise.resolve(null)
  return new Promise((resolve) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => resolve(null)
    img.src = src
  })
}

/** Draws text cut to fit width, with an ellipsis. */
function fit(ctx: CanvasRenderingContext2D, text: string, x: number, y: number, width: number) {
  let t = text
  while (t.length > 1 && ctx.measureText(t).width > width) t = t.slice(0, -1)
  ctx.fillText(t === text ? t : `${t.trimEnd()}…`, x, y)
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath()
  ctx.roundRect(x, y, w, h, r)
}

/** Draws the card and returns it as a PNG. */
export async function drawWrappedCard({ recap, roomId, roomName, title, date, nameOf }: CardInput): Promise<Blob> {
  const canvas = document.createElement('canvas')
  canvas.width = W
  canvas.height = H
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('no canvas')
  const font = getComputedStyle(document.body).fontFamily || 'system-ui, sans-serif'
  const f = (weight: number, px: number) => `${weight} ${px}px ${font}`

  // A deep night sky, warming toward the bottom.
  const bg = ctx.createLinearGradient(0, 0, W * 0.4, H)
  bg.addColorStop(0, '#120a2e')
  bg.addColorStop(0.55, '#2a0f45')
  bg.addColorStop(1, '#5b1a3a')
  ctx.fillStyle = bg
  ctx.fillRect(0, 0, W, H)
  const glow = ctx.createRadialGradient(W * 0.85, H * 0.12, 0, W * 0.85, H * 0.12, W * 0.8)
  glow.addColorStop(0, 'rgba(251,191,36,0.28)')
  glow.addColorStop(1, 'rgba(251,191,36,0)')
  ctx.fillStyle = glow
  ctx.fillRect(0, 0, W, H)

  let y = PAD + 40
  ctx.fillStyle = 'rgba(253,230,138,0.95)'
  ctx.font = f(700, 34)
  ctx.fillText('SYNCPHONY WRAPPED', PAD, y)
  y += 96
  ctx.fillStyle = '#fff'
  ctx.font = f(800, 92)
  fit(ctx, title, PAD, y, W - PAD * 2)
  y += 64
  ctx.fillStyle = 'rgba(255,255,255,0.7)'
  ctx.font = f(500, 40)
  fit(ctx, `${roomName} · ${date}`, PAD, y, W - PAD * 2)

  // The numbers.
  y += 110
  const s = recap.stats
  const tiles: [string, string][] = [
    [String(s.plays), s.plays === 1 ? 'song' : 'songs'],
    [formatListening(s.listeningMs), 'listening'],
    [String(s.people.length), s.people.length === 1 ? 'person' : 'people'],
  ]
  const tw = (W - PAD * 2 - 40) / 3
  tiles.forEach(([v, label], i) => {
    const x = PAD + i * (tw + 20)
    roundRect(ctx, x, y, tw, 190, 36)
    ctx.fillStyle = 'rgba(255,255,255,0.08)'
    ctx.fill()
    ctx.fillStyle = '#fff'
    ctx.font = f(800, v.length > 6 ? 46 : 64)
    fit(ctx, v, x + 32, y + 100, tw - 48)
    ctx.fillStyle = 'rgba(255,255,255,0.6)'
    ctx.font = f(500, 32)
    ctx.fillText(label, x + 32, y + 152)
  })
  y += 190 + 90

  // Song of the night.
  const song = recap.night?.songOfTheNight
  if (song) {
    const art = await loadImage(queueArtworkUrl(roomId, song.item, 400))
    const size = 260
    roundRect(ctx, PAD, y, size, size, 32)
    ctx.save()
    ctx.clip()
    if (art) ctx.drawImage(art, PAD, y, size, size)
    else {
      ctx.fillStyle = 'rgba(255,255,255,0.12)'
      ctx.fillRect(PAD, y, size, size)
    }
    ctx.restore()
    const x = PAD + size + 48
    const w = W - x - PAD
    ctx.fillStyle = 'rgba(253,230,138,0.95)'
    ctx.font = f(700, 30)
    ctx.fillText('👑 SONG OF THE NIGHT', x, y + 50)
    ctx.fillStyle = '#fff'
    ctx.font = f(800, 54)
    fit(ctx, song.item.track.title, x, y + 124, w)
    ctx.fillStyle = 'rgba(255,255,255,0.7)'
    ctx.font = f(500, 38)
    fit(ctx, song.item.track.artists.join(', '), x, y + 178, w)
    ctx.fillStyle = 'rgba(255,255,255,0.6)'
    ctx.font = f(500, 32)
    fit(ctx, `♥ ${song.hearts} · picked by ${nameOf(song.item.addedBy)}`, x, y + 232, w)
    y += size + 100
  }

  // Top artists.
  if (s.topArtists.length > 0) {
    ctx.fillStyle = 'rgba(255,255,255,0.6)'
    ctx.font = f(700, 30)
    ctx.fillText('TOP ARTISTS', PAD, y)
    y += 70
    s.topArtists.slice(0, 3).forEach((a, i) => {
      ctx.fillStyle = '#fff'
      ctx.font = f(800, 50)
      fit(ctx, `${i + 1}  ${a.name}`, PAD, y, W - PAD * 2)
      y += 72
    })
    y += 40
  }

  // Genres.
  if (recap.genres.length > 0) {
    ctx.fillStyle = 'rgba(255,255,255,0.6)'
    ctx.font = f(700, 30)
    ctx.fillText('THE MIX', PAD, y)
    y += 40
    const top = Math.max(...recap.genres.map((g) => g.plays))
    for (const g of recap.genres.slice(0, 3)) {
      const bw = ((W - PAD * 2) * 0.62 * g.plays) / top
      roundRect(ctx, PAD, y, Math.max(bw, 24), 44, 22)
      ctx.fillStyle = 'rgba(244,114,182,0.75)'
      ctx.fill()
      ctx.fillStyle = '#fff'
      ctx.font = f(600, 34)
      fit(ctx, g.name, PAD + Math.max(bw, 24) + 24, y + 34, W - PAD * 2 - bw - 24)
      y += 66
    }
    y += 30
  }

  // Who brought it.
  const lines = [
    recap.topAdder && `Most songs: ${nameOf(recap.topAdder.userId)} (${recap.topAdder.count})`,
    recap.mostHearted && `Most hearts: ${nameOf(recap.mostHearted.userId)} (${recap.mostHearted.count})`,
    recap.streak && `Longest streak: ${nameOf(recap.streak.userId)}, ${recap.streak.count} in a row`,
  ].filter((l): l is string => !!l)
  ctx.font = f(600, 38)
  for (const l of lines) {
    if (y > H - PAD - 80) break
    ctx.fillStyle = 'rgba(255,255,255,0.88)'
    fit(ctx, l, PAD, y + 30, W - PAD * 2)
    y += 64
  }

  ctx.fillStyle = 'rgba(255,255,255,0.45)'
  ctx.font = f(600, 30)
  ctx.fillText('syncphony', PAD, H - PAD)

  return new Promise((resolve, reject) => canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('no image'))), 'image/png'))
}

/** Shares the card where the device can, or downloads it. */
export async function shareCard(blob: Blob, filename: string) {
  const file = new File([blob], filename, { type: 'image/png' })
  if (navigator.canShare?.({ files: [file] })) {
    try {
      await navigator.share({ files: [file] })
      return 'shared'
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return 'cancelled'
    }
  }
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 10_000)
  return 'downloaded'
}
