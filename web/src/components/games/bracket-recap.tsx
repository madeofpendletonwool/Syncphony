import { useQuery } from '@tanstack/react-query'
import { Trophy } from 'lucide-react'
import { champion, roundName, type QueueGame } from '@/lib/queue-games'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

/**
 * A night's bracket battle in its recap (MAD-796): the champion, then
 * each round's results, the final first.
 */
export function BracketRecap({ game }: { game: QueueGame }) {
  const users = useQuery(usersQuery).data
  const b = game.bracket
  if (!b) return null
  const name = (id: string) => users?.find((u) => u.id === id)?.displayName ?? 'Someone'
  const champ = champion(game)
  const rounds = b.rounds.map((r, i) => ({ r, i })).reverse()
  return (
    <div className="flex flex-col gap-3 px-2 py-2">
      {champ ? (
        <p className="flex items-center gap-2 text-sm font-semibold">
          <Trophy className="size-4 shrink-0 text-primary" />
          <span className="truncate">{champ.title}</span>
          <span className="shrink-0 font-normal text-muted-foreground">· {name(champ.userId)}</span>
        </p>
      ) : (
        <p className="text-sm text-muted-foreground">The bracket didn’t get to its final.</p>
      )}
      {rounds.map(({ r, i }) => {
        const played = r.filter((m) => m.state === 'done' && !m.bye && m.winner >= 0)
        if (played.length === 0) return null
        return (
          <div key={i} className="flex flex-col gap-1">
            <p className="text-caption font-medium tracking-wide text-muted-foreground uppercase">{roundName(i, b.rounds.length)}</p>
            {played.map((m, k) => {
              const won = game.entries[m.winner]
              const lostAt = m.winner === m.a ? m.b : m.a
              const lost = lostAt >= 0 ? game.entries[lostAt] : undefined
              const [wh, lh] = m.winner === m.a ? [m.heartsA, m.heartsB] : [m.heartsB, m.heartsA]
              return (
                <p key={k} className="flex min-w-0 items-center gap-1.5 text-caption">
                  <span className="truncate font-medium">{won.title}</span>
                  <span className="shrink-0 text-muted-foreground">beat</span>
                  <span className={cn('truncate text-muted-foreground')}>{lost?.title ?? 'a bye'}</span>
                  <span className="ml-auto shrink-0 text-muted-foreground tabular-nums">
                    {m.walkover ? 'walkover' : `${wh}–${lh} ♥${m.toss ? ', on a coin toss' : ''}`}
                  </span>
                </p>
              )
            })}
          </div>
        )
      })}
    </div>
  )
}
