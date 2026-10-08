import { lineParts, type GameRound } from '@/lib/games'
import { cn } from '@/lib/utils'

/**
 * A round's quoted lyric line, with its blanks as gaps to fill. From the
 * reveal on, the blanks fill in with the words.
 */
export function RoundLine({ round, text, className }: { round: GameRound; text: string; className?: string }) {
  const revealed = round.state === 'reveal' || round.state === 'done'
  const words = revealed && round.kind === 'lyrics' ? (round.correct ?? '').split(' ') : []
  let n = 0
  return (
    <p className={className}>
      {lineParts(text).map((p, i) => {
        if (!p.blank) return <span key={i}>{p.text}</span>
        const word = words[n++]
        return (
          <span
            key={i}
            className={cn(
              'mx-[0.1em] inline-block min-w-[3.2em] rounded-[0.35em] px-[0.3em] text-center not-italic transition-colors duration-500',
              word ? 'bg-success/15 font-semibold text-success' : 'border-b-2 border-current/50 bg-foreground/8 text-transparent',
            )}
          >
            {word ?? '____'}
          </span>
        )
      })}
    </p>
  )
}
