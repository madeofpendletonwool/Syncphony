import { useMutation, useQuery } from '@tanstack/react-query'
import { AudioLines, Check, Dices, Flame, LoaderCircle, Minus, Pause, Plus, Send, Speaker, VolumeX, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Slider } from '@/components/ui/slider'
import { useMe } from '@/lib/auth'
import {
  answerRound,
  clipNumber,
  GAMES,
  gameRoundQuery,
  gameScoresQuery,
  isOpen,
  myAnswers,
  rememberAnswer,
  roundArtworkUrl,
  setGamesMuted,
  setStandings,
  splitPrompt,
  streakOf,
  useGamesMuted,
  type GameAnswer,
  type GameRound,
} from '@/lib/games'
import { tap } from '@/lib/haptics'
import { easeOutExpo } from '@/lib/motion'
import { useCurrentRoom } from '@/lib/room'
import { speakerState } from '@/lib/speaker'
import { useStore } from '@/lib/store'
import { cn } from '@/lib/utils'
import { usersQuery } from '@/lib/users'
import { useServerNow } from '@/hooks/use-server-now'
import { Countdown } from './countdown'
import { RoundLine } from './round-line'

/**
 * A game round on a phone (MAD-785): a compact answer sheet that slides up
 * over now playing while a round is up, and goes away on its own. Ambient
 * rounds start folded, so they're easy to ignore. Muted on this device, or
 * when the room keeps rounds on the big screen, it waits as a small chip.
 */
export function RoundSheet() {
  const { room } = useCurrentRoom()
  const round = useQuery({ ...gameRoundQuery(room?.id ?? ''), enabled: !!room }).data
  const muted = useGamesMuted()
  const [dismissed, setDismissed] = useState<string>()
  const [folded, setFolded] = useState<Record<string, boolean>>({})

  if (!room || !round) return null
  // Folded by default when it's ambient, or kept off phones.
  const quiet = muted || round.tvOnly || dismissed === round.id
  const isFolded = folded[round.id] ?? (round.mode === 'ambient' || quiet)

  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-[calc(var(--spacing-nav)+var(--spacing-mini)+env(safe-area-inset-bottom)+1.5rem)] z-[55] px-3">
      <AnimatePresence mode="wait">
        {isFolded ? (
          <Chip
            key={`chip-${round.id}`}
            round={round}
            onOpen={() => {
              setFolded((f) => ({ ...f, [round.id]: false }))
              setDismissed(undefined)
            }}
          />
        ) : (
          <Sheet
            key={round.id}
            roomId={room.id}
            round={round}
            onFold={() => setFolded((f) => ({ ...f, [round.id]: true }))}
            onDismiss={() => setDismissed(round.id)}
          />
        )}
      </AnimatePresence>
    </div>
  )
}

function Chip({ round, onOpen }: { round: GameRound; onOpen: () => void }) {
  const answered = !!useStore(myAnswers)[round.id]
  return (
    <motion.button
      type="button"
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: 12 }}
      transition={{ duration: 0.3, ease: easeOutExpo }}
      onClick={onOpen}
      className="glass-strong pointer-events-auto mx-auto flex max-w-2xl items-center gap-2 rounded-full py-2 pr-4 pl-3 text-sm shadow-float outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <Dices className="size-4 text-primary" />
      <span className="font-medium">{GAMES[round.kind].label}</span>
      <span className="text-muted-foreground">
        {round.state === 'reveal' ? '· See the answer' : answered ? '· Answered' : '· Tap to play'}
      </span>
    </motion.button>
  )
}

function Sheet({ roomId, round, onFold, onDismiss }: { roomId: string; round: GameRound; onFold: () => void; onDismiss: () => void }) {
  const me = useMe()
  const now = useServerNow()
  const mine = useStore(myAnswers)[round.id]
  const speaking = useStore(speakerState).status !== 'off' && round.hides.includes('song')
  const answer = useMutation({
    mutationFn: (a: GameAnswer) => answerRound(roomId, round.id, a),
    onMutate: (a) => {
      tap()
      rememberAnswer(round.id, a)
    },
  })
  const open = isOpen(round, now)
  const canPlay = !me.guest || round.guests
  const revealed = round.state === 'reveal' || round.state === 'done'
  const result = round.results?.find((r) => r.userId === me.id)

  // A right answer gets a tap when it's revealed.
  useEffect(() => {
    if (revealed && result?.correct) tap()
  }, [revealed, result?.correct])

  return (
    <motion.section
      initial={{ opacity: 0, y: 40 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: 40 }}
      transition={{ duration: 0.4, ease: easeOutExpo }}
      aria-label={`${GAMES[round.kind].label} round`}
      className="glass-strong pointer-events-auto mx-auto flex max-w-md flex-col gap-3 rounded-3xl p-4 shadow-float"
    >
      <header className="flex items-center gap-2">
        <Dices className="size-4 text-primary" />
        <span className="text-caption font-semibold tracking-wide text-muted-foreground uppercase">{GAMES[round.kind].label}</span>
        {round.set && (
          <span className="text-caption text-muted-foreground tabular-nums">
            {round.set.number} of {round.set.size}
          </span>
        )}
        <span className="ml-auto">
          <Countdown round={round} now={now} />
        </span>
        <Button size="icon-sm" variant="ghost" aria-label="Fold the round away" onClick={onFold}>
          <X />
        </Button>
      </header>

      <Prompt round={round} />

      {round.kind === 'tune' && !revealed ? (
        <Listening round={round} />
      ) : (
        round.stopsMusic &&
        round.state === 'open' && (
          <p className="flex items-center gap-2 rounded-xl bg-primary/12 px-3 py-2 text-caption font-medium text-primary">
            <Pause className="size-3.5 shrink-0" />
            The music’s stopped. It comes back on the line.
          </p>
        )
      )}

      {speaking && !revealed && (
        <p className="flex items-center gap-2 rounded-xl bg-muted/60 px-3 py-2 text-caption text-muted-foreground">
          <Speaker className="size-3.5 shrink-0" />
          This phone is the speaker: its lock screen shows the song. No peeking.
        </p>
      )}

      {canPlay ? (
        <Answer round={round} mine={mine} open={open} revealed={revealed} pending={answer.isPending} onAnswer={(a) => answer.mutate(a)} />
      ) : (
        <p className="text-sm text-muted-foreground">Guests watch this one. Shout it out!</p>
      )}

      <AnimatePresence initial={false}>
        {answer.error && !revealed && (
          <motion.p initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} className="text-caption text-destructive">
            {errorMessage(answer.error)}
          </motion.p>
        )}
      </AnimatePresence>

      {revealed && (
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.5, ease: easeOutExpo }}
          className="flex flex-col gap-1 rounded-2xl bg-muted/60 px-3 py-2.5"
        >
          {round.tune && <TuneCover round={round} />}
          <p className="text-sm font-semibold">{round.reveal ?? round.correct}</p>
          {round.detail && <p className="text-caption text-muted-foreground">{round.detail}</p>}
          {result ? (
            <p className={cn('text-caption', result.correct ? 'text-success' : 'text-muted-foreground')}>
              {resultLine(result)}
            </p>
          ) : (
            mine === undefined && canPlay && <p className="text-caption text-muted-foreground">You sat this one out</p>
          )}
          {round.topic === 'higher_lower' && <Streak roomId={roomId} userId={me.id} />}
          {round.set && <SetLine round={round} userId={me.id} />}
        </motion.div>
      )}

      <footer className="flex items-center justify-between gap-2 text-caption text-muted-foreground">
        <span>
          {round.answered.length === 0 ? 'Nobody has answered yet' : `${round.answered.length} answered`}
        </span>
        <button
          type="button"
          onClick={() => {
            setGamesMuted(true)
            onDismiss()
          }}
          className="flex items-center gap-1 rounded-lg px-1 transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <VolumeX className="size-3.5" />
          Mute games here
        </button>
      </footer>
    </motion.section>
  )
}

/** Name that tune: which clip is playing, before the reveal. */
function Listening({ round }: { round: GameRound }) {
  const n = clipNumber(round)
  return (
    <p className="flex items-center gap-2 rounded-xl bg-primary/12 px-3 py-2 text-caption font-medium text-primary">
      <AudioLines className={cn('size-3.5 shrink-0', round.state === 'open' && 'animate-pulse')} />
      {round.state === 'announce' ? 'Listen closely: the music stops for a clip' : `Clip ${n}: sooner scores more, one guess each`}
    </p>
  )
}

/** The tune's cover, at the reveal. */
function TuneCover({ round }: { round: GameRound }) {
  const [failed, setFailed] = useState(false)
  const src = roundArtworkUrl(round, 160)
  if (!src || failed) return null
  return <img src={src} alt="" onError={() => setFailed(true)} className="mb-1 size-16 rounded-xl object-cover shadow-float" />
}

/** Where you stand in the set, and its winner once it's over. */
function SetLine({ round, userId }: { round: GameRound; userId: string }) {
  const users = useQuery(usersQuery).data
  const set = setStandings(round)
  if (!set) return null
  const place = set.board.findIndex((p) => p.userId === userId)
  const name = (id: string) => (id === userId ? 'You' : (users?.find((u) => u.id === id)?.displayName ?? 'Someone'))
  if (set.over) {
    return (
      <p className="text-caption font-medium">
        {set.winners.length === 0 ? 'Nobody got one. Rematch?' : `${set.winners.map(name).join(' and ')} won the set!`}
      </p>
    )
  }
  return (
    <p className="text-caption text-muted-foreground tabular-nums">
      {place < 0 ? `Tune ${set.number} of ${set.size}` : `${ordinal(place + 1)} in the set · ${set.board[place].points.toLocaleString()}`}
    </p>
  )
}

function ordinal(n: number) {
  const s = n % 100 >= 11 && n % 100 <= 13 ? 'th' : (['th', 'st', 'nd', 'rd'][n % 10] ?? 'th')
  return `${n}${s}`
}

type Result = NonNullable<GameRound['results']>[number]

/** How you did, in words. */
function resultLine(r: Result) {
  if (r.correct) return r.closest && r.number !== undefined ? `Spot on! +${r.points}` : `Right! +${r.points}`
  if (r.closest && r.points > 0) return `Closest in the room: +${r.points}`
  return r.points > 0 ? `Close: +${r.points}` : 'Not this time'
}

/** Your higher-or-lower streak, once the scores come in. */
function Streak({ roomId, userId }: { roomId: string; userId: string }) {
  const scores = useQuery(gameScoresQuery(roomId)).data
  const { streak, best } = streakOf(scores, userId)
  if (streak === 0 && best === 0) return null
  return (
    <p className="flex items-center gap-1.5 text-caption text-muted-foreground">
      <Flame className={cn('size-3.5', streak > 0 && 'text-primary')} />
      {streak > 0 ? `${streak} in a row` : 'Streak over'}
      {best > streak && <span>· best {best}</span>}
    </p>
  )
}

/** The prompt: a question, then a quoted line on its own line, if any. */
function Prompt({ round, className }: { round: GameRound; className?: string }) {
  const { question, line } = splitPrompt(round.prompt)
  return (
    <div className={className}>
      <p className="text-base leading-snug font-semibold text-balance">{question}</p>
      {line && <RoundLine round={round} text={line} className="mt-1 text-sm text-muted-foreground italic" />}
    </div>
  )
}

function Answer({
  round,
  mine,
  open,
  revealed,
  pending,
  onAnswer,
}: {
  round: GameRound
  mine?: GameAnswer
  open: boolean
  revealed: boolean
  pending: boolean
  onAnswer: (a: GameAnswer) => void
}) {
  // Name that tune takes one guess, no changes.
  const locked = round.kind === 'tune' && mine !== undefined
  // Choices, for choice rounds and number rounds shown as choices.
  if (round.choices.length > 0) {
    return (
      <div className="grid grid-cols-2 gap-2">
        {round.choices.map((c, i) => {
          const picked = mine?.choice === i
          const right = revealed && round.correctIndex === i
          return (
            <button
              key={i}
              type="button"
              disabled={!open || pending || locked}
              aria-pressed={picked}
              onClick={() => onAnswer({ choice: i })}
              className={cn(
                'flex min-h-12 items-center justify-center gap-1.5 rounded-2xl border border-border px-3 py-2 text-center text-sm font-medium transition-[background-color,border-color,opacity] outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
                picked && !revealed && 'border-primary bg-primary/15',
                right && 'border-success bg-success/15',
                revealed && picked && !right && 'border-destructive/60 bg-destructive/10',
                !open && !revealed && 'opacity-60',
                revealed && !right && !picked && 'opacity-50',
              )}
            >
              {right && <Check className="size-4 shrink-0 text-success" />}
              <span className="line-clamp-2">{c}</span>
            </button>
          )
        })}
      </div>
    )
  }
  if (round.answer === 'number' && round.min !== undefined && round.max !== undefined) {
    return <YearSlider round={round} mine={mine} open={open} pending={pending} onAnswer={onAnswer} />
  }
  return <TypedAnswer round={round} mine={mine} open={open && !locked} pending={pending} onAnswer={onAnswer} />
}

/** Guess the year: a slider across the decades, nudged a year at a time. */
function YearSlider({
  round,
  mine,
  open,
  pending,
  onAnswer,
}: {
  round: GameRound
  mine?: GameAnswer
  open: boolean
  pending: boolean
  onAnswer: (a: GameAnswer) => void
}) {
  // Only for rounds with the slider's ends.
  const min = round.min ?? 0
  const max = round.max ?? 0
  const [year, setYear] = useState(mine?.number ?? Math.round((min + max) / 2))
  const nudge = (d: number) => setYear((y) => Math.min(max, Math.max(min, y + d)))
  const sent = mine?.number === year
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <Button size="icon" variant="ghost" aria-label="A year earlier" disabled={!open || year <= min} onClick={() => nudge(-1)}>
          <Minus />
        </Button>
        <output aria-live="polite" className="text-4xl font-bold tracking-tight tabular-nums">
          {year}
        </output>
        <Button size="icon" variant="ghost" aria-label="A year later" disabled={!open || year >= max} onClick={() => nudge(1)}>
          <Plus />
        </Button>
      </div>
      <Slider aria-label="The year" min={min} max={max} step={1} value={[year]} disabled={!open} onValueChange={([y]) => setYear(y)} />
      <div className="flex justify-between text-caption text-muted-foreground tabular-nums">
        <span>{min}</span>
        <span>{max}</span>
      </div>
      <Button disabled={!open || pending || sent} onClick={() => onAnswer({ number: year })}>
        {pending ? <LoaderCircle className="animate-spin" /> : sent ? <Check /> : null}
        {sent ? `Locked in ${year}` : mine?.number !== undefined ? `Change to ${year}` : `Lock in ${year}`}
      </Button>
    </div>
  )
}

const PLACEHOLDERS: Partial<Record<GameRound['kind'], string>> = {
  lyrics: 'The missing words',
  finish_lyric: 'How does it go on?',
  tune: 'The song’s title',
}

function TypedAnswer({
  round,
  mine,
  open,
  pending,
  onAnswer,
}: {
  round: GameRound
  mine?: GameAnswer
  open: boolean
  pending: boolean
  onAnswer: (a: GameAnswer) => void
}) {
  const number = round.answer === 'number'
  const [draft, setDraft] = useState(mine ? String(mine.number ?? mine.text ?? '') : '')
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const v = draft.trim()
    if (!v) return
    onAnswer(number ? { number: Number(v) } : { text: v })
  }
  const sent = mine && String(mine.number ?? mine.text ?? '') === draft.trim()
  return (
    <form onSubmit={submit} className="flex items-center gap-2">
      <Input
        value={draft}
        onChange={(e) => setDraft(number ? e.target.value.replace(/\D/g, '').slice(0, 4) : e.target.value)}
        inputMode={number ? 'numeric' : 'text'}
        placeholder={number ? 'Year' : (PLACEHOLDERS[round.kind] ?? 'Your answer')}
        maxLength={number ? 4 : 200}
        autoComplete="off"
        autoCapitalize="off"
        disabled={!open}
        aria-label="Your answer"
      />
      <Button type="submit" size="icon" disabled={!open || pending || !draft.trim()} aria-label={sent ? 'Answer sent' : 'Send answer'}>
        {pending ? <LoaderCircle className="animate-spin" /> : sent ? <Check /> : <Send />}
      </Button>
    </form>
  )
}
