import { Activity, Lightbulb, RotateCcw, Sparkles, X } from 'lucide-react'
import { Popover } from 'radix-ui'
import { useState, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useBeat } from '@/hooks/use-beat'
import { SCENES, useScene } from '@/hooks/use-scene'
import {
  BEAT_DEFAULTS,
  EFFECTS,
  beatSettings,
  beatSource,
  calibrateTap,
  currentBeat,
  gridVersion,
  lightsOpen,
  nudge,
  resetGrid,
  scaleTempo,
  setBeatSettings,
  shiftDownbeat,
  tap,
  visualizerOpen,
  type BeatOrigin,
  type Effect,
} from '@/lib/beat'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'

type Tab = 'look' | 'effects' | 'beat'

/**
 * The beat lab (MAD-778): everything about how the app moves with the
 * music, on this device. Turn it all off, set the overall intensity, pick
 * the backdrop, tune or switch off each effect, tell it how late this
 * device's sound is, and fix a song's beat by tapping along. Always a tap
 * away: floating top right, or `inline` in a header (now playing's). Its
 * button blinks on the beat.
 */
export function BeatLab({ inline = false }: { inline?: boolean }) {
  const [open, setOpen] = useState(false)
  const beat = useBeat<HTMLButtonElement>()
  const { level } = useStore(beatSettings)

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button
          ref={beat}
          type="button"
          aria-label={open ? 'Close beat lab' : 'Beat lab'}
          onPointerDown={(e) => e.stopPropagation()}
          className={cn(
            'glass-strong z-[60] flex size-10 items-center justify-center rounded-full outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
            !inline && 'fixed top-[calc(env(safe-area-inset-top)+0.5rem)] right-2 shadow-float',
          )}
        >
          {open ? (
            <X className="size-4" />
          ) : (
            <span className="relative flex size-4 items-center justify-center">
              <Activity className={cn('size-4', level !== 'off' && 'opacity-40')} />
              {/* Blinks on the beat: is it in time? */}
              <span
                aria-hidden
                className="absolute size-2 rounded-full bg-primary"
                style={{ opacity: 'calc(var(--beat, 0) * 1.6)' }}
              />
            </span>
          )}
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          align="end"
          sideOffset={8}
          collisionPadding={12}
          className="z-[60] w-[min(21rem,calc(100vw-1.5rem))] outline-none"
        >
          <Panel />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

function Panel() {
  const settings = useStore(beatSettings)
  const [tab, setTab] = useState<Tab>('look')
  const beat = useBeat<HTMLDivElement>()
  const on = settings.level !== 'off'

  return (
    <div ref={beat} className="glass-strong flex max-h-[min(36rem,80dvh)] flex-col rounded-2xl text-sm shadow-float">
      <div className="flex items-center justify-between gap-3 px-4 pt-4">
        <p className="font-medium">Beat lab</p>
        <Switch label="Move with the music" checked={on} onChange={(v) => setBeatSettings({ level: v ? 'subtle' : 'off' })} />
      </div>
      {!on ? (
        <p className="px-4 pt-2 pb-4 text-muted-foreground">Everything holds still. Switch it back on to move with the music.</p>
      ) : (
        <>
          <ToggleGroup
            type="single"
            value={tab}
            onValueChange={(v) => v && setTab(v as Tab)}
            className="mx-4 mt-3 grid w-auto grid-cols-3"
          >
            <ToggleGroupItem value="look">Look</ToggleGroupItem>
            <ToggleGroupItem value="effects">Effects</ToggleGroupItem>
            <ToggleGroupItem value="beat">Beat</ToggleGroupItem>
          </ToggleGroup>
          <div className="min-h-0 space-y-5 overflow-y-auto p-4">
            {tab === 'look' && <Look />}
            {tab === 'effects' && <Effects />}
            {tab === 'beat' && <Beat />}
          </div>
          <div className="flex justify-end border-t border-glass-border px-4 py-2">
            <Button size="xs" variant="ghost" onClick={() => beatSettings.set(BEAT_DEFAULTS)}>
              <RotateCcw data-icon="inline-start" />
              Reset everything
            </Button>
          </div>
        </>
      )}
    </div>
  )
}

function Look() {
  const { intensity, scene: setting } = useStore(beatSettings)
  const scene = useScene()
  return (
    <>
      <Field label="Intensity" value={intensityLabel(intensity)}>
        <Slider min={0.5} max={1.75} step={0.05} value={[intensity]} onValueChange={([v]) => setBeatSettings({ intensity: v })} />
      </Field>
      <Field label="Backdrop" value={<span className="capitalize">{setting === 'auto' ? `Auto: ${scene}` : scene}</span>}>
        <div className="flex flex-wrap gap-1.5">
          {['auto', ...SCENES].map((s) => (
            <Button
              key={s}
              size="xs"
              variant={setting === s ? 'default' : 'secondary'}
              className="capitalize"
              onClick={() => setBeatSettings({ scene: s })}
            >
              {s}
            </Button>
          ))}
        </div>
        <p className="text-caption text-muted-foreground">Auto picks a backdrop that suits each song.</p>
      </Field>
      <Field label="Visualizer">
        <Button variant="secondary" className="w-full" onClick={() => visualizerOpen.set(true)}>
          <Sparkles data-icon="inline-start" />
          Open the visualizer
        </Button>
        <p className="text-caption text-muted-foreground">The song's visuals, full screen. ← and → change the show.</p>
      </Field>
      <Field label="Lights">
        <Button variant="secondary" className="w-full" onClick={() => lightsOpen.set(true)}>
          <Lightbulb data-icon="inline-start" />
          Turn this phone into a light
        </Button>
        <p className="text-caption text-muted-foreground">
          The screen flashes the song's colors on the downbeat, in time with every other phone in the room doing the same.
        </p>
      </Field>
    </>
  )
}

function Effects() {
  const { effects } = useStore(beatSettings)
  return (
    <>
      {(Object.keys(EFFECTS) as Effect[]).map((k) => (
        <Field key={k} label={EFFECTS[k]} value={effects[k] === 0 ? 'Off' : `${Math.round(effects[k] * 100)}%`}>
          <div className="flex items-center gap-2">
            <Slider
              min={0}
              max={2}
              step={0.1}
              value={[effects[k]]}
              onValueChange={([v]) => setBeatSettings({ effects: { [k]: v } })}
              className="flex-1"
            />
            <Switch
              label={`${EFFECTS[k]} on`}
              checked={effects[k] > 0}
              onChange={(v) => setBeatSettings({ effects: { [k]: v ? 1 : 0 } })}
            />
          </div>
        </Field>
      ))}
    </>
  )
}

function Beat() {
  const { delayMs } = useStore(beatSettings)
  const { mapKey, np } = useStore(beatSource)
  useStore(gridVersion)
  const now = currentBeat()
  const loading = !!np?.itemId && mapKey !== np.itemId
  const [heard, setHeard] = useState(0)
  const [taps, setTaps] = useState(0)
  const canCalibrate = now.origin === 'analysed' && !!np && !np.paused

  return (
    <>
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="text-lg leading-tight font-semibold tabular-nums">
            {!np ? 'Nothing playing' : now.bpm > 0 ? `${Math.round(now.bpm)} BPM` : 'No steady beat'}
          </p>
          {np && <p className="text-caption text-muted-foreground">{loading ? 'Listening to the song…' : origins[now.origin]}</p>}
        </div>
        <div className="flex items-center gap-1.5" aria-hidden>
          <Dot v="var(--downbeat, 0)" big />
          <Dot v="var(--beat, 0)" />
        </div>
      </div>

      <Field label="This device's delay" value={`${delayMs > 0 ? '+' : ''}${delayMs} ms`}>
        <Slider min={-300} max={600} step={10} value={[delayMs]} onValueChange={([v]) => setBeatSettings({ delayMs: v })} />
        <Button
          variant="secondary"
          className="w-full"
          disabled={!canCalibrate}
          onPointerDown={(e) => {
            e.preventDefault()
            setHeard(calibrateTap() ?? 0)
          }}
        >
          {heard > 0 && heard < 4 ? `Keep tapping (${heard})` : 'Tap along to measure it'}
        </Button>
        <p className="text-caption text-muted-foreground">
          Sound through a Bluetooth speaker arrives late. Tap on the beat you hear and the visuals wait for it.
        </p>
      </Field>

      <Field label="Fix this song's beat" value={now.origin === 'tapped' ? 'Tapped' : undefined}>
        <Button
          className="h-12 w-full"
          disabled={!np}
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
          <Button size="xs" variant="secondary" disabled={now.bpm <= 0} onClick={() => scaleTempo(1 - 1 / now.bpm)}>
            −1
          </Button>
          <Button size="xs" variant="secondary" disabled={now.bpm <= 0} onClick={() => scaleTempo(1 + 1 / now.bpm)}>
            +1
          </Button>
          <Button size="xs" variant="secondary" onClick={shiftDownbeat}>
            Move 1
          </Button>
          <Button size="xs" variant="ghost" disabled={now.origin !== 'tapped'} onClick={resetGrid}>
            Reset
          </Button>
        </div>
      </Field>
    </>
  )
}

function Field({ label, value, children }: { label: string; value?: ReactNode; children: ReactNode }) {
  return (
    <div className="space-y-2">
      <span className="flex justify-between gap-3 text-muted-foreground">
        {label}
        {value !== undefined && <span className="text-foreground tabular-nums">{value}</span>}
      </span>
      {children}
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
