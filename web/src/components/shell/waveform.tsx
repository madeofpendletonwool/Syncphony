import { useMemo } from 'react'
import { beatSettings, beatSource } from '@/lib/beat'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'

const BARS = 72

/**
 * The song's shape above the seek bar (MAD-777): its loudness from the beat
 * map, filled in the album color up to where it's playing. A flat line
 * until the map arrives (it keeps its space, so nothing jumps), and
 * nothing if the beat lab turns it off.
 */
export function Waveform({ itemId, positionMs, durationMs, className }: {
  itemId?: string
  positionMs: number
  durationMs: number
  className?: string
}) {
  const { map, mapKey } = useStore(beatSource)
  const { level, effects } = useStore(beatSettings)
  const ours = map && mapKey === itemId ? map : null

  const heights = useMemo(() => {
    if (!ours || ours.loudness.length === 0) return Array<number>(BARS).fill(0.12)
    const per = ours.loudness.length / BARS
    const out: number[] = []
    for (let b = 0; b < BARS; b++) {
      let peak = 0
      for (let i = Math.floor(b * per); i < Math.min(ours.loudness.length, Math.floor((b + 1) * per)); i++) {
        peak = Math.max(peak, ours.loudness[i])
      }
      // Quiet parts still show a sliver, so the line reads as a whole.
      out.push(0.12 + 0.88 * (peak / 255) ** 1.6)
    }
    return out
  }, [ours])

  if (level === 'off' || effects.waveform === 0) return null
  const played = durationMs > 0 ? Math.min(1, positionMs / durationMs) : 0
  const bars = (
    <svg viewBox={`0 0 ${BARS * 3} 24`} preserveAspectRatio="none" className="absolute inset-0 size-full">
      {heights.map((h, i) => (
        <rect key={i} x={i * 3 + 0.5} y={12 - h * 12} width={2} height={h * 24} rx={1} />
      ))}
    </svg>
  )
  return (
    <div aria-hidden className={cn('relative h-7 transition-opacity duration-500', !ours && 'opacity-40', className)}>
      <div className="absolute inset-0 fill-foreground/15">{bars}</div>
      <div
        className="absolute inset-0 fill-(--pal-text) transition-[clip-path] duration-300 ease-linear"
        style={{ clipPath: `inset(0 ${(1 - played) * 100}% 0 0)` }}
      >
        {bars}
      </div>
    </div>
  )
}
