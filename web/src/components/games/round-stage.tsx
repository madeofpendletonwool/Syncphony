import { useQuery } from '@tanstack/react-query'
import { ArrowRight, Check, Dices, Disc3, Flame, Pause, Trophy } from 'lucide-react'
import { useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { UserAvatar } from '@/components/user-avatar'
import { useServerNow } from '@/hooks/use-server-now'
import { GAMES, gameRoundQuery, gameScoresQuery, remaining, roundArtworkUrl, splitPrompt, type GameRound } from '@/lib/games'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo, spring } from '@/lib/motion'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'
import { RoundLine } from './round-line'

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
      {isYear(round) && (revealed ? <Timeline round={round} /> : <YearRange round={round} />)}
      {revealed && round.other && <Covers round={round} />}
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
        {round.stopsMusic && round.state === 'open' && (
          <span className="flex items-center gap-[0.4vw] text-primary">
            <Pause className="size-[2.6vh]" /> Music paused
          </span>
        )}
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
  const { question, line } = splitPrompt(round.prompt)
  return (
    <div>
      <p className={cn('leading-tight font-bold text-balance', big ? 'text-[clamp(1.6rem,3.2vw,3.6rem)]' : 'text-[clamp(1.1rem,1.8vw,2rem)]')}>
        {question}
      </p>
      {line && (
        <RoundLine
          round={round}
          text={line}
          className={cn('mt-[1vh] text-muted-foreground italic', big ? 'text-[clamp(1.2rem,2.2vw,2.4rem)]' : 'text-[clamp(0.95rem,1.4vw,1.5rem)]')}
        />
      )}
    </div>
  )
}

/** Whether a round is guessed on the year slider. */
function isYear(round: GameRound) {
  return round.answer === 'number' && round.min !== undefined && round.max !== undefined
}

/** While answers are open: the slider's span, as phones see it. */
function YearRange({ round }: { round: GameRound }) {
  return (
    <div className="flex items-center gap-[1vw] text-[clamp(0.9rem,1.3vw,1.4rem)] text-muted-foreground tabular-nums">
      <span>{round.min}</span>
      <span className="h-[0.6vh] flex-1 rounded-full bg-foreground/12" />
      <span>{round.max}</span>
    </div>
  )
}

/**
 * Guess the year's reveal: everyone's guess on a timeline, with the real
 * year marked. The closest glow.
 */
function Timeline({ round }: { round: GameRound }) {
  const users = useQuery(usersQuery).data
  // A year round always has its slider's ends (isYear).
  const min = round.min ?? 0
  const max = round.max ?? 0
  const span = Math.max(1, max - min)
  const at = (y: number) => `${((Math.min(max, Math.max(min, y)) - min) / span) * 100}%`
  const year = Number(round.correct)
  const guesses = (round.results ?? []).filter((r) => r.number !== undefined)
  // Guesses on the same year stack up.
  const stack = new Map<number, number>()
  const decades = Array.from({ length: Math.floor(max / 10) - Math.ceil(min / 10) + 1 }, (_, i) => (Math.ceil(min / 10) + i) * 10)
  return (
    <div className="relative mt-[5vh] mb-[1vh] h-[11vh]">
      <div className="absolute inset-x-0 top-[6vh] h-[0.6vh] rounded-full bg-foreground/12" />
      {decades.map((d) => (
        <span
          key={d}
          style={{ left: at(d) }}
          className="absolute top-[7.6vh] -translate-x-1/2 text-[clamp(0.7rem,1vw,1.05rem)] text-muted-foreground tabular-nums"
        >
          {d}
        </span>
      ))}
      {Number.isFinite(year) && (
        <motion.div
          initial={{ opacity: 0, scaleY: 0 }}
          animate={{ opacity: 1, scaleY: 1 }}
          transition={{ duration: 0.7, ease: easeOutExpo }}
          style={{ left: at(year) }}
          className="absolute -top-[4vh] bottom-[3vh] flex w-0 origin-bottom flex-col items-center"
        >
          <span className="rounded-full bg-success px-[0.8vw] py-[0.3vh] text-[clamp(0.9rem,1.4vw,1.5rem)] font-bold text-background tabular-nums">
            {year}
          </span>
          <span className="w-[0.3vw] flex-1 bg-success" />
        </motion.div>
      )}
      {guesses.map((r, i) => {
        const u = users?.find((x) => x.id === r.userId)
        const n = stack.get(r.number!) ?? 0
        stack.set(r.number!, n + 1)
        return (
          <motion.span
            key={r.userId}
            initial={{ opacity: 0, y: -12 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ ...spring, delay: 0.25 + i * 0.06 }}
            style={{ left: at(r.number!), top: `${4.2 - n * 3.2}vh` }}
            title={`${u?.displayName ?? 'Someone'}: ${r.number}`}
            className={cn('absolute -translate-x-1/2 rounded-full', r.closest && 'ring-[0.4vh] ring-success')}
          >
            {u ? <UserAvatar user={u} className="size-[4vh] text-[1.5vh] ring-2 ring-background" /> : <span className="block size-[2vh] rounded-full bg-foreground/50" />}
          </motion.span>
        )
      })}
    </div>
  )
}

/** Sample detective's reveal: the two songs' covers, side by side. */
function Covers({ round }: { round: GameRound }) {
  const other = round.other!
  const mine = `/api/rooms/${encodeURIComponent(round.roomId)}/queue/${encodeURIComponent(round.itemId)}/artwork?size=300`
  const samples = round.topic !== 'sampled_by'
  const left = { src: mine, title: 'This song' }
  const right = { src: roundArtworkUrl(round), title: other.title, artist: other.artist }
  const [a, b] = samples ? [left, right] : [right, left]
  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.7, ease: easeOutExpo }}
      className="flex items-center justify-center gap-[2vw]"
    >
      <Cover {...a} />
      <span className="flex flex-col items-center gap-[0.6vh] text-[clamp(0.8rem,1.1vw,1.15rem)] font-semibold tracking-[0.18em] text-muted-foreground uppercase">
        <ArrowRight className="size-[3.5vh]" />
        samples
      </span>
      <Cover {...b} />
    </motion.div>
  )
}

function Cover({ src, title, artist }: { src?: string; title: string; artist?: string }) {
  const [failed, setFailed] = useState(false)
  return (
    <figure className="flex w-[16vh] flex-col items-center gap-[0.8vh] text-center">
      {src && !failed ? (
        <img src={src} alt="" onError={() => setFailed(true)} className="size-[16vh] rounded-[2vh] object-cover shadow-float" />
      ) : (
        <span className="grid size-[16vh] place-items-center rounded-[2vh] bg-foreground/8">
          <Disc3 className="size-[6vh] text-muted-foreground" />
        </span>
      )}
      <figcaption className="w-full">
        <p className="truncate text-[clamp(0.85rem,1.2vw,1.3rem)] font-semibold">{title}</p>
        {artist && <p className="truncate text-[clamp(0.75rem,1vw,1.1rem)] text-muted-foreground">{artist}</p>}
      </figcaption>
    </figure>
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
      {round.detail && <span className="mt-[0.4vh] block text-[clamp(0.8rem,1.1vw,1.2rem)] font-normal text-muted-foreground">{round.detail}</span>}
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
        {round.detail && <p className="text-[clamp(0.9rem,1.3vw,1.4rem)] text-muted-foreground">{round.detail}</p>}
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
          {round.topic === 'higher_lower' && scores?.best && (
            <li className="flex items-center gap-[0.6vw] text-[clamp(0.85rem,1.2vw,1.3rem)] text-muted-foreground">
              <Flame className="size-[2.4vh] text-primary" />
              Best streak: {users?.find((x) => x.id === scores.best!.userId)?.displayName ?? 'Someone'}, {scores.best.count} in a row
            </li>
          )}
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
