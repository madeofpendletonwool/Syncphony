import { useQuery } from '@tanstack/react-query'
import { Check, Dices, Trophy } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { UserAvatar } from '@/components/user-avatar'
import { useServerNow } from '@/hooks/use-server-now'
import { GAMES, gameRoundQuery, gameScoresQuery, remaining, type GameRound } from '@/lib/games'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo, spring } from '@/lib/motion'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

const LETTERS = ['A', 'B', 'C', 'D', 'E', 'F']

/**
 * A game round on the big screen (MAD-785): the question, a countdown,
 * who's answered, then the reveal and the board. It sits over the stage
 * or the visualizer; phones are the controllers. Ambient rounds take a
 * corner, so the music keeps the screen.
 */
export function RoundStage({ roomId }: { roomId: string }) {
  const round = useQuery(gameRoundQuery(roomId)).data
  return (
    <AnimatePresence>
      {round && (round.mode === 'ambient' ? <Corner key={round.id} round={round} /> : <Panel key={round.id} round={round} />)}
    </AnimatePresence>
  )
}

function Panel({ round }: { round: GameRound }) {
  const revealed = round.state === 'reveal' || round.state === 'done'
  return (
    <motion.div
      initial={{ opacity: 0, y: 60, scale: 0.97 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: 40 }}
      transition={{ duration: 0.8, ease: easeOutExpo }}
      role="status"
      aria-live="polite"
      className="glass-strong fixed inset-x-[6vw] bottom-[5vh] z-[55] flex flex-col gap-[2.4vh] rounded-[4vh] p-[3.5vh] shadow-float"
    >
      <Header round={round} />
      <Question round={round} big />
      {round.choices.length > 0 && <Choices round={round} big />}
      {revealed ? <Reveal round={round} /> : <Answered round={round} />}
    </motion.div>
  )
}

function Corner({ round }: { round: GameRound }) {
  const revealed = round.state === 'reveal' || round.state === 'done'
  return (
    <motion.div
      initial={{ opacity: 0, x: 40 }}
      animate={{ opacity: 1, x: 0 }}
      exit={{ opacity: 0, x: 40 }}
      transition={{ duration: 0.8, ease: easeOutExpo }}
      role="status"
      aria-live="polite"
      className="glass-strong fixed top-[11vh] right-[3vw] z-[55] flex w-[32vw] flex-col gap-[1.6vh] rounded-[3vh] p-[2.4vh] shadow-float"
    >
      <Header round={round} />
      <Question round={round} />
      {round.choices.length > 0 && <Choices round={round} />}
      {revealed ? <RevealLine round={round} /> : <Answered round={round} />}
    </motion.div>
  )
}

function Header({ round }: { round: GameRound }) {
  const now = useServerNow()
  const left = remaining(round, now)
  const seconds = round.state === 'open' ? Math.max(0, Math.ceil((Date.parse(round.closesAt) - now) / 1000)) : undefined
  return (
    <div className="flex items-center gap-[1vw]">
      <Dices className="size-[3vh] text-primary" />
      <span className="text-[clamp(0.9rem,1.3vw,1.4rem)] font-semibold tracking-[0.18em] text-muted-foreground uppercase">
        {GAMES[round.kind].label}
      </span>
      <span className="ml-auto flex items-center gap-[0.8vw] text-[clamp(1rem,1.6vw,1.7rem)] font-semibold tabular-nums">
        {round.state === 'announce' && <span className="text-muted-foreground">Get your phones out</span>}
        {seconds !== undefined && <span className={cn(seconds <= 5 && 'text-destructive')}>{seconds}</span>}
        {round.state !== 'announce' && round.state !== 'open' && <span className="text-muted-foreground">Time’s up</span>}
        <Ring value={left} />
      </span>
    </div>
  )
}

/** A ring that empties as the answer window runs out. */
function Ring({ value }: { value: number }) {
  const r = 16
  const c = 2 * Math.PI * r
  return (
    <svg viewBox="0 0 40 40" className="size-[4.5vh] -rotate-90" aria-hidden>
      <circle cx="20" cy="20" r={r} fill="none" strokeWidth="4" className="stroke-foreground/15" />
      <circle
        cx="20"
        cy="20"
        r={r}
        fill="none"
        strokeWidth="4"
        strokeLinecap="round"
        strokeDasharray={c}
        strokeDashoffset={c * (1 - value)}
        className="stroke-primary transition-[stroke-dashoffset] duration-300 ease-linear"
      />
    </svg>
  )
}

function Question({ round, big }: { round: GameRound; big?: boolean }) {
  const [question, ...rest] = round.prompt.split('\n')
  return (
    <div>
      <p className={cn('leading-tight font-bold text-balance', big ? 'text-[clamp(1.6rem,3.2vw,3.6rem)]' : 'text-[clamp(1.1rem,1.8vw,2rem)]')}>
        {question}
      </p>
      {rest.length > 0 && (
        <p className={cn('mt-[1vh] text-muted-foreground italic', big ? 'text-[clamp(1.2rem,2.2vw,2.4rem)]' : 'text-[clamp(0.95rem,1.4vw,1.5rem)]')}>
          {rest.join(' ')}
        </p>
      )}
    </div>
  )
}

function Choices({ round, big }: { round: GameRound; big?: boolean }) {
  const revealed = round.state === 'reveal' || round.state === 'done'
  return (
    <ol className={cn('grid gap-[1vw]', big ? 'grid-cols-4' : 'grid-cols-2')}>
      {round.choices.map((c, i) => {
        const right = revealed && round.correctIndex === i
        return (
          <motion.li
            key={i}
            animate={{ opacity: revealed && !right ? 0.35 : 1, scale: right ? 1.03 : 1 }}
            transition={spring}
            className={cn(
              'flex items-center gap-[0.8vw] rounded-[2vh] border-2 border-transparent bg-foreground/8 p-[1.4vh]',
              right && 'border-success bg-success/15',
            )}
          >
            <span className="grid size-[4vh] shrink-0 place-items-center rounded-[1.2vh] bg-foreground/10 text-[clamp(0.9rem,1.3vw,1.4rem)] font-bold">
              {right ? <Check className="size-[2.4vh] text-success" /> : LETTERS[i]}
            </span>
            <span className={cn('line-clamp-2 font-semibold', big ? 'text-[clamp(1rem,1.6vw,1.8rem)]' : 'text-[clamp(0.85rem,1.1vw,1.2rem)]')}>{c}</span>
          </motion.li>
        )
      })}
    </ol>
  )
}

/** Who's answered so far, as their avatars pop in. */
function Answered({ round }: { round: GameRound }) {
  const users = useQuery(usersQuery).data
  return (
    <div className="flex min-h-[5vh] items-center gap-[1vw]">
      <div className="flex -space-x-[0.6vw]">
        <AnimatePresence initial={false}>
          {round.answered.map((id) => {
            const u = users?.find((x) => x.id === id)
            return (
              u && (
                <motion.span key={id} initial={{ scale: 0, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} transition={spring}>
                  <UserAvatar user={u} className="size-[5vh] text-[1.8vh] ring-2 ring-background" />
                </motion.span>
              )
            )
          })}
        </AnimatePresence>
      </div>
      <span className="text-[clamp(0.9rem,1.3vw,1.4rem)] text-muted-foreground">
        {round.state === 'announce'
          ? round.tvOnly
            ? 'Shout it out'
            : 'Answers open in a moment'
          : round.answered.length === 0
            ? round.tvOnly
              ? 'Shout it out'
              : 'Answer on your phone'
            : `${round.answered.length} answered`}
      </span>
    </div>
  )
}

function RevealLine({ round }: { round: GameRound }) {
  return (
    <motion.p
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.6, ease: easeOutExpo }}
      className="text-[clamp(1rem,1.5vw,1.6rem)] font-semibold"
    >
      {round.reveal ?? round.correct}
    </motion.p>
  )
}

/** The answer, who got it, and tonight's board. */
function Reveal({ round }: { round: GameRound }) {
  const users = useQuery(usersQuery).data
  const scores = useQuery({ ...gameScoresQuery(round.roomId), enabled: round.scores === 'board' }).data
  const results = (round.results ?? []).filter((r) => r.points > 0).slice(0, 5)
  const board = round.scores === 'board' ? (scores?.players ?? []).slice(0, 5) : []
  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.7, ease: easeOutExpo }}
      className="grid grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)] gap-[3vw]"
    >
      <div className="flex flex-col gap-[1.4vh]">
        <p className="text-[clamp(1.2rem,2vw,2.2rem)] font-bold">{round.reveal ?? round.correct}</p>
        {results.length === 0 ? (
          <p className="text-[clamp(0.9rem,1.3vw,1.4rem)] text-muted-foreground">Nobody got it this time</p>
        ) : (
          <ul className="flex flex-wrap gap-[1vw]">
            {results.map((r) => {
              const u = users?.find((x) => x.id === r.userId)
              return (
                <li key={r.userId} style={laneStyle(u?.color)} className="flex items-center gap-[0.6vw] rounded-full bg-foreground/8 py-[0.6vh] pr-[1vw] pl-[0.6vh]">
                  {u && <UserAvatar user={u} className="size-[4vh] text-[1.5vh]" />}
                  <span className="text-[clamp(0.85rem,1.2vw,1.3rem)] font-semibold">{u?.displayName ?? 'Someone'}</span>
                  <span className="text-[clamp(0.85rem,1.2vw,1.3rem)] text-(--lane) tabular-nums">+{r.points}</span>
                </li>
              )
            })}
          </ul>
        )}
      </div>
      {board.length > 0 && (
        <ol className="flex flex-col gap-[0.8vh]">
          <li className="flex items-center gap-[0.6vw] text-[clamp(0.8rem,1.1vw,1.15rem)] font-semibold tracking-[0.18em] text-muted-foreground uppercase">
            <Trophy className="size-[2.4vh]" /> Tonight
          </li>
          {board.map((p, i) => {
            const u = users?.find((x) => x.id === p.userId)
            return (
              <motion.li key={p.userId} layout transition={spring} style={laneStyle(u?.color)} className="flex items-center gap-[0.8vw]">
                <span className="w-[2vw] text-[clamp(0.9rem,1.3vw,1.4rem)] font-bold text-muted-foreground tabular-nums">{i + 1}</span>
                {u && <UserAvatar user={u} className="size-[4vh] text-[1.5vh]" />}
                <span className="min-w-0 flex-1 truncate text-[clamp(0.9rem,1.3vw,1.4rem)] font-semibold">{u?.displayName ?? 'Someone'}</span>
                <span className="text-[clamp(0.9rem,1.3vw,1.4rem)] text-(--lane) tabular-nums">{p.points.toLocaleString()}</span>
              </motion.li>
            )
          })}
        </ol>
      )}
    </motion.div>
  )
}
