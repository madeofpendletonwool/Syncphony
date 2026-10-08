import { Activity, ChevronDown } from 'lucide-react'
import { Popover } from 'radix-ui'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import { useBeat } from '@/hooks/use-beat'
import { SCENES, useScene } from '@/hooks/use-scene'
import {
  beatSettings,
  beatSource,
  currentBeat,
  gridVersion,
  nudge,
  resetGrid,
  scaleTempo,
  shiftDownbeat,
  tap,
  type BeatOrigin,
} from '@/lib/beat'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'

/**
 * The beat lab (MAD-778): how the app moves with the music, on this
 * device. Turn it off, make it calmer or stronger, pick the backdrop, and
 * fix the beat by tapping along when the analysis got a song wrong.
 * Opens from now playing; its button blinks on the beat.
 */
export function BeatLab() {
  const [open, setOpen] = useState(false)
  const beat = useBeat<HTMLButtonElement>()
  const { level } = useStore(beatSettings)

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <Button
          ref={beat}
          size="icon"
          variant={open ? 'default' : 'glass'}
          aria-label="Beat lab"
          onPointerDown={(e) => e.stopPropagation()}
        >
          <span className="relative flex items-center justify-center">
            <Activity className={cn(level !== 'off' && 'opacity-50')} />
            {/* Blinks on the beat: is it in time? */}
            <span
              aria-hidden
              className="absolute size-1.5 rounded-full bg-current"
              style={{ opacity: 'calc(var(--beat, 0) * 1.6)' }}
            />
          </span>
        </Button>
      </Popover.Trigger>
      <Popover.Content
        align="end"
        sideOffset={8}
        collisionPadding={12}
        onPointerDown={(e) => e.stopPropagation()}
        className="z-[60] w-[min(20rem,calc(100vw-1.5rem))] outline-none"
      >
        <Panel />
      </Popover.Content>
    </Popover.Root>
  )
}

function Panel() {
  const settings = useStore(beatSettings)
  const { map, mapKey, np } = useStore(beatSource)
  useStore(gridVersion)
  const beat = useBeat<HTMLDivElement>()
  const scene = useScene()
  const now = currentBeat()
  const [fixing, setFixing] = useState(false)
  const set = (next: Partial<typeof settings>) => beatSettings.set((s) => ({ ...s, ...next }))
  const on = settings.level !== 'off'
  const loading = !!np?.itemId && mapKey !== np.itemId && !map

  return (
    <div ref={beat} className="glass-strong max-h-[70dvh] space-y-4 overflow-y-auto rounded-2xl p-4 text-sm shadow-float">
      <div className="flex items-center justify-between gap-3">
        <p className="font-medium">Beat lab</p>
        <Switch label="Move with the music" checked={on} onChange={(v) => set({ level: v ? 'subtle' : 'off' })} />
      </div>

      {on && (
        <>
          <div className="flex items-center justify-between gap-3">
            <div className="min-w-0">
              <p className="text-lg leading-tight font-semibold tabular-nums">
                {now.bpm > 0 ? `${Math.round(now.bpm)} BPM` : 'No steady beat'}
              </p>
              <p className="text-caption text-muted-foreground">{loading ? 'Listening to the song…' : origins[now.origin]}</p>
            </div>
            <div className="flex items-center gap-1.5" aria-hidden>
              <Dot v="var(--downbeat, 0)" big />
              <Dot v="var(--beat, 0)" />
            </div>
          </div>

          <label className="block space-y-2">
            <span className="flex justify-between text-muted-foreground">
              Intensity <span>{intensityLabel(settings.intensity)}</span>
            </span>
            <Slider
              min={0.5}
              max={1.75}
              step={0.05}
              value={[settings.intensity]}
              onValueChange={([intensity]) => set({ intensity })}
            />
          </label>

          <div className="space-y-2">
            <span className="flex justify-between text-muted-foreground">
              Backdrop <span className="capitalize">{settings.scene === 'auto' ? `Auto: ${scene}` : scene}</span>
            </span>
            <div className="flex flex-wrap gap-1.5">
              {['auto', ...SCENES].map((s) => (
                <Button
                  key={s}
                  size="xs"
                  variant={settings.scene === s ? 'default' : 'secondary'}
                  className="capitalize"
                  onClick={() => set({ scene: s })}
                >
                  {s}
                </Button>
              ))}
            </div>
          </div>

          <div className="space-y-2">
            <button
              type="button"
              aria-expanded={fixing}
              onClick={() => setFixing((f) => !f)}
              className="flex w-full items-center justify-between rounded-lg text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              Off the beat? Fix it
              <ChevronDown className={cn('size-4 transition-transform', fixing && 'rotate-180')} />
            </button>
            {fixing && <Fix bpm={now.bpm} tapped={now.origin === 'tapped'} />}
          </div>
        </>
      )}
    </div>
  )
}

/** Tap along to set this song's beat, then nudge it. Only on this device. */
function Fix({ bpm, tapped }: { bpm: number; tapped: boolean }) {
  const [taps, setTaps] = useState(0)
  return (
    <div className="space-y-2">
      <Button
        className="h-14 w-full text-base"
        onPointerDown={(e) => {
          // On press, not release: a click lands a beat late.
          e.preventDefault()
          setTaps(tap() ?? 0)
        }}
      >
        Tap on the beat {taps > 0 && taps < 3 ? `(${taps})` : ''}
      </Button>
      <p className="text-caption text-muted-foreground">
        Start on a &ldquo;one&rdquo; and tap at least 4 times. Fixes this song on this device.
      </p>
      <div className="grid grid-cols-4 gap-1.5">
        <Button size="xs" variant="secondary" onClick={() => nudge(-20)}>
          −20ms
        </Button>
        <Button size="xs" variant="secondary" onClick={() => nudge(20)}>
          +20ms
        </Button>
        <Button size="xs" variant="secondary" onClick={() => scaleTempo(0.5)}>
          ½×
        </Button>
        <Button size="xs" variant="secondary" onClick={() => scaleTempo(2)}>
          2×
        </Button>
        <Button size="xs" variant="secondary" disabled={bpm <= 0} onClick={() => scaleTempo(1 - 1 / bpm)}>
          −1
        </Button>
        <Button size="xs" variant="secondary" disabled={bpm <= 0} onClick={() => scaleTempo(1 + 1 / bpm)}>
          +1
        </Button>
        <Button size="xs" variant="secondary" onClick={shiftDownbeat}>
          Move 1
        </Button>
        <Button size="xs" variant="ghost" disabled={!tapped} onClick={resetGrid}>
          Reset
        </Button>
      </div>
    </div>
  )
}

const origins: Record<BeatOrigin, string> = {
  analysed: 'From the song',
  tapped: 'Tapped on this device',
  steady: 'Moving with its loudness',
  default: 'A guess: this song has no beat map',
}

function intensityLabel(v: number) {
  if (v < 0.8) return 'Calm'
  if (v <= 1.2) return 'Normal'
  if (v <= 1.5) return 'Strong'
  return 'Max'
}

function Dot({ v, big }: { v: string; big?: boolean }) {
  return (
    <span
      className={cn('rounded-full bg-primary', big ? 'size-3' : 'size-2')}
      style={{ opacity: `calc(0.15 + ${v} * 0.85)` }}
    />
  )
}
