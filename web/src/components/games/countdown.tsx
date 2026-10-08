import { secondsUntil, type GameRound } from '@/lib/games'
import { cn } from '@/lib/utils'

/** Where a round is: getting ready, seconds left, or time's up. */
export function Countdown({ round, now, className }: { round: GameRound; now: number; className?: string }) {
  if (round.state === 'announce') {
    return <span className={cn('text-caption text-muted-foreground tabular-nums', className)}>Get ready… {secondsUntil(round.opensAt, now)}</span>
  }
  if (round.state === 'open') {
    const left = secondsUntil(round.closesAt, now)
    return (
      <span className={cn('text-sm font-semibold tabular-nums', left <= 5 ? 'text-destructive' : 'text-foreground', className)} aria-live="off">
        {left}s
      </span>
    )
  }
  return <span className={cn('text-caption text-muted-foreground', className)}>Time’s up</span>
}
