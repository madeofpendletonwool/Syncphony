import { createFileRoute } from '@tanstack/react-router'
import { Disc3, Heart, Plus } from 'lucide-react'
import type { ReactNode } from 'react'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { LaneDot, UserAvatar } from '@/components/user-avatar'
import { LANE_PALETTE, laneStyle } from '@/lib/lane'
import { player, positionAt, usePlayer, type NowPlaying } from '@/lib/now-playing'

export const Route = createFileRoute('/design')({
  component: Design,
})

// A living style guide: tokens, primitives and a demo player that drives the
// shell (and the album-art accent) without a server.
function Design() {
  const { nowPlaying } = usePlayer()

  return (
    <>
      <PageHeader title="Design" subtitle="Tokens and primitives. Start the demo to see the accent follow the art." />
      <div className="flex flex-col gap-8">
        <Section title="Demo player">
          <div className="flex flex-wrap gap-2">
            <Button onClick={startDemo}>
              <Disc3 data-icon="inline-start" />
              {nowPlaying ? 'Restart demo' : 'Start demo'}
            </Button>
            <Button variant="secondary" disabled={!nowPlaying} onClick={stopDemo}>
              Stop
            </Button>
          </div>
        </Section>

        <Section title="Type">
          <p className="text-display">Display</p>
          <p className="text-title">Title</p>
          <p className="text-headline">Headline</p>
          <p>Body text for descriptions and lists.</p>
          <p className="text-caption text-muted-foreground">Caption · 3:42</p>
        </Section>

        <Section title="Surfaces">
          <div className="grid grid-cols-3 gap-3 text-center text-caption">
            <div className="rounded-2xl bg-card p-4">card</div>
            <div className="glass rounded-2xl p-4">glass</div>
            <div className="glass-strong rounded-2xl p-4">glass-strong</div>
          </div>
          <div className="grid grid-cols-4 gap-3 text-center text-caption">
            {SWATCHES.map(([name, bg]) => (
              <div key={name} className="flex flex-col items-center gap-1.5">
                <span className={`h-10 w-full rounded-xl border ${bg}`} />
                {name}
              </div>
            ))}
          </div>
        </Section>

        <Section title="Lane colors">
          <div className="flex flex-wrap gap-3">
            {LANE_PALETTE.map((color, i) => (
              <UserAvatar key={color} user={{ displayName: DEMO_NAMES[i], color }} ring />
            ))}
          </div>
          <div className="flex flex-wrap gap-2">
            {LANE_PALETTE.slice(0, 6).map((color, i) => (
              <Badge key={color} variant="lane" style={laneStyle(color)}>
                <LaneDot color={color} />
                {DEMO_NAMES[i]}
              </Badge>
            ))}
          </div>
        </Section>

        <Section title="Buttons">
          <div className="flex flex-wrap items-center gap-2">
            <Button>
              <Plus data-icon="inline-start" />
              Add to my lane
            </Button>
            <Button variant="secondary">Secondary</Button>
            <Button variant="glass">Glass</Button>
            <Button variant="outline">Outline</Button>
            <Button variant="ghost">Ghost</Button>
            <Button variant="destructive">Remove</Button>
            <Button size="icon" variant="ghost" aria-label="Like">
              <Heart />
            </Button>
          </div>
        </Section>

        <Section title="Inputs and badges">
          <Input placeholder="Songs, albums, artists" />
          <div className="flex flex-wrap gap-2">
            <Badge>Default</Badge>
            <Badge variant="secondary">Secondary</Badge>
            <Badge variant="outline">Navidrome</Badge>
            <Badge variant="glass">Glass</Badge>
            <Badge variant="destructive">Unavailable</Badge>
          </div>
          <div className="flex items-center gap-3">
            <Skeleton className="size-12 rounded-xl" />
            <div className="flex flex-1 flex-col gap-2">
              <Skeleton className="h-4 w-2/3" />
              <Skeleton className="h-3 w-1/3" />
            </div>
          </div>
        </Section>
      </div>
    </>
  )
}

// Spelled out so Tailwind sees the class names.
const SWATCHES = [
  ['primary', 'bg-primary'],
  ['secondary', 'bg-secondary'],
  ['muted', 'bg-muted'],
  ['accent', 'bg-accent'],
] as const

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-caption font-semibold tracking-widest text-muted-foreground uppercase">{title}</h2>
      {children}
    </section>
  )
}

const DEMO_NAMES = ['Collin', 'Ada', 'Grace', 'Linus', 'Margaret', 'Ken', 'Barbara', 'Dennis', 'Radia', 'Alan', 'Frances', 'Edsger']

const DEMO_TRACKS = [
  { title: 'Neon Tide', artists: ['Glass Harbor'], album: 'Lowlight', stops: ['#ff4d8d', '#7b2ff7', '#12063b'], who: 0 },
  { title: 'Paper Satellites', artists: ['The Quiet Orbit'], album: 'Afterglow', stops: ['#14f1d9', '#0a5c8a', '#04121f'], who: 1 },
  { title: 'Ember Season', artists: ['Marigold', 'June Arcade'], album: 'Ember', stops: ['#ffd23f', '#ff6b1a', '#3a0d02'], who: 2 },
]

function demoArtwork(stops: string[]) {
  const canvas = document.createElement('canvas')
  canvas.width = canvas.height = 600
  const ctx = canvas.getContext('2d')
  if (!ctx) return undefined
  const g = ctx.createLinearGradient(0, 0, 600, 600)
  stops.forEach((s, i) => g.addColorStop(i / (stops.length - 1), s))
  ctx.fillStyle = g
  ctx.fillRect(0, 0, 600, 600)
  ctx.globalCompositeOperation = 'overlay'
  for (let i = 0; i < 6; i++) {
    ctx.beginPath()
    ctx.arc(300, 300, 60 + i * 45, 0, Math.PI * 2)
    ctx.strokeStyle = `rgba(255,255,255,${0.35 - i * 0.05})`
    ctx.lineWidth = 14
    ctx.stroke()
  }
  return canvas.toDataURL('image/jpeg', 0.85)
}

function demoItem(i: number): NowPlaying {
  const t = DEMO_TRACKS[i]
  return {
    track: {
      provider: 'demo',
      trackId: `demo-${i}`,
      title: t.title,
      artists: t.artists,
      album: t.album,
      durationMs: 184_000 + i * 31_000,
      explicit: false,
    },
    artworkUrl: demoArtwork(t.stops),
    requester: { id: `u${t.who}`, displayName: DEMO_NAMES[t.who], color: LANE_PALETTE[t.who] },
    paused: false,
    positionMs: 0,
    at: Date.now(),
  }
}

let demoIndex = 0

function play(i: number) {
  demoIndex = (i + DEMO_TRACKS.length) % DEMO_TRACKS.length
  player.set({ nowPlaying: demoItem(demoIndex) })
}

function update(fn: (np: NowPlaying, now: number) => Partial<NowPlaying>) {
  const np = player.get().nowPlaying
  if (!np) return
  const now = Date.now()
  player.set({ nowPlaying: { ...np, ...fn(np, now), at: now } })
}

function startDemo() {
  play(0)
  player.set({
    commands: {
      toggle: () => update((np, now) => ({ paused: !np.paused, positionMs: positionAt(np, now) })),
      next: () => play(demoIndex + 1),
      previous: () => play(demoIndex - 1),
      seek: (positionMs) => update(() => ({ positionMs })),
    },
  })
}

function stopDemo() {
  player.set({ nowPlaying: null, commands: {} })
}
