import { useBeat } from '@/hooks/use-beat'
import { cn } from '@/lib/utils'

// Each bar's period and offset differ, so the pattern never visibly repeats.
const bars = [
  { duration: '1.1s', delay: '-0.4s' },
  { duration: '0.8s', delay: '-0.2s' },
  { duration: '1.3s', delay: '-0.9s' },
  { duration: '0.95s', delay: '-0.6s' },
]

/**
 * Bouncing bars: audio is playing here. Settles low while paused. While the
 * beat engine is live, each bar follows one of its bands instead.
 */
export function Equalizer({ playing, className }: { playing: boolean; className?: string }) {
  const beat = useBeat<HTMLSpanElement>()
  return (
    <span ref={beat} aria-hidden className={cn('flex h-4 items-end gap-[3px]', className)}>
      {bars.map((b, i) => (
        <span
          key={i}
          className="eq-bar h-full w-[3px] rounded-full bg-current"
          style={
            {
              '--eq-duration': b.duration,
              '--eq-delay': b.delay,
              '--eq': `var(--b${i})`,
              animationPlayState: playing ? 'running' : 'paused',
            } as React.CSSProperties
          }
        />
      ))}
    </span>
  )
}
