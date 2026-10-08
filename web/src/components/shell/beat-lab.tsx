import { Activity, X } from 'lucide-react'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import { useBeat } from '@/hooks/use-beat'
import { SCENES, useScene } from '@/hooks/use-scene'
import {
  beatSettings,
  currentGrid,
  gridVersion,
  nudge,
  resetGrid,
  scaleTempo,
  shiftDownbeat,
  tap,
} from '@/lib/beat'
import { usePlayer } from '@/lib/now-playing'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'

/**
 * Dev-only tuning for the beat prototype (MAD-772): tap along to set the
 * song's grid, fake the energy, or run without music. Goes away once the
 * server sends beat maps.
 */
export function BeatLab() {
  const [open, setOpen] = useState(false)
  const settings = useStore(beatSettings)
  useStore(gridVersion)
  const { nowPlaying } = usePlayer()
  const [taps, setTaps] = useState(0)
  const beat = useBeat<HTMLDivElement>()
  const grid = currentGrid()
  const scene = useScene()
  const set = (next: Partial<typeof settings>) => beatSettings.set((s) => ({ ...s, ...next }))

  return (
    <div
      ref={beat}
      className="fixed top-[calc(env(safe-area-inset-top)+0.5rem)] right-2 z-[60] flex flex-col items-end gap-2"
    >
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-label={open ? 'Close beat lab' : 'Open beat lab'}
        className="glass-strong flex size-10 items-center justify-center rounded-full shadow-float outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        {open ? (
          <X className="size-4" />
        ) : (
          <span className="relative flex size-4 items-center justify-center">
            <Activity className="size-4 opacity-40" />
            {/* Blinks on the beat: is the grid in time? */}
            <span className="absolute size-2 rounded-full bg-primary" style={{ opacity: 'calc(var(--beat, 0) * 1.6)' }} />
          </span>
        )}
      </button>

      {open && (
        <div className="glass-strong w-72 space-y-4 rounded-2xl p-4 text-sm shadow-float">
          <div className="flex items-center justify-between">
            <p className="font-medium">Beat lab</p>
            <div className="flex items-center gap-1.5" aria-hidden>
              <Dot v="var(--downbeat, 0)" big />
              <Dot v="var(--beat, 0)" />
            </div>
          </div>

          <Row label="Music-reactive">
            <Switch
              label="Music-reactive"
              checked={settings.level !== 'off'}
              onChange={(on) => set({ level: on ? 'subtle' : 'off' })}
            />
          </Row>
          <Row label="Run without music">
            <Switch label="Run without music" checked={settings.freeRun} onChange={(freeRun) => set({ freeRun })} />
          </Row>

          <div className="space-y-2">
            <div className="flex items-baseline justify-between">
              <span className="text-muted-foreground">
                {nowPlaying && !nowPlaying.paused ? 'This song' : settings.freeRun ? 'Free run' : 'Nothing playing'}
              </span>
              <span className="text-lg font-semibold tabular-nums">{Math.round(grid.bpm)} BPM</span>
            </div>
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
              Start on a &ldquo;one&rdquo; and tap at least 4 times. Without taps a song runs at the default tempo,
              out of phase.
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
              <Button size="xs" variant="secondary" onClick={() => scaleTempo(1 - 1 / grid.bpm)}>
                −1
              </Button>
              <Button size="xs" variant="secondary" onClick={() => scaleTempo(1 + 1 / grid.bpm)}>
                +1
              </Button>
              <Button size="xs" variant="secondary" onClick={shiftDownbeat}>
                Move 1
              </Button>
              <Button size="xs" variant="ghost" onClick={resetGrid}>
                Reset
              </Button>
            </div>
          </div>

          <div className="space-y-2">
            <span className="flex justify-between text-muted-foreground">
              Backdrop <span className="capitalize">{settings.scene === 'auto' ? `auto: ${scene}` : scene}</span>
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

          <label className="block space-y-2">
            <span className="flex justify-between text-muted-foreground">
              Energy <span className="tabular-nums">{Math.round(settings.energy * 100)}%</span>
            </span>
            <Slider
              min={0}
              max={1}
              step={0.01}
              value={[settings.energy]}
              onValueChange={([energy]) => set({ energy })}
            />
          </label>
        </div>
      )}
    </div>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span>{label}</span>
      {children}
    </div>
  )
}

function Dot({ v, big }: { v: string; big?: boolean }) {
  return (
    <span
      className={cn('rounded-full bg-primary', big ? 'size-3' : 'size-2')}
      style={{ opacity: `calc(0.15 + ${v} * 0.85)` }}
    />
  )
}
