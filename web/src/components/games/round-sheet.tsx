import { useMutation, useQuery } from '@tanstack/react-query'
import { Check, Dices, LoaderCircle, Send, Speaker, VolumeX, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useState, type FormEvent } from 'react'
import { errorMessage } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useMe } from '@/lib/auth'
import {
  answerRound,
  GAMES,
  gameRoundQuery,
  isOpen,
  myAnswers,
  rememberAnswer,
  setGamesMuted,
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
import { useServerNow } from '@/hooks/use-server-now'
import { Countdown } from './countdown'

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
        <span className="ml-auto">
          <Countdown round={round} now={now} />
        </span>
        <Button size="icon-sm" variant="ghost" aria-label="Fold the round away" onClick={onFold}>
          <X />
        </Button>
      </header>

      <Prompt text={round.prompt} />

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
          <p className="text-sm font-semibold">{round.reveal ?? round.correct}</p>
          {result ? (
            <p className={cn('text-caption', result.correct ? 'text-success' : 'text-muted-foreground')}>
              {result.correct ? `Right! +${result.points}` : result.points > 0 ? `Close: +${result.points}` : 'Not this time'}
            </p>
          ) : (
            mine === undefined && canPlay && <p className="text-caption text-muted-foreground">You sat this one out</p>
          )}
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

/** The prompt: a question, then a quoted line on its own line, if any. */
function Prompt({ text, className }: { text: string; className?: string }) {
  const [question, ...rest] = text.split('\n')
  return (
    <div className={className}>
      <p className="text-base leading-snug font-semibold text-balance">{question}</p>
      {rest.length > 0 && <p className="mt-1 text-sm text-muted-foreground italic">{rest.join(' ')}</p>}
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
              disabled={!open || pending}
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
  return <TypedAnswer round={round} mine={mine} open={open} pending={pending} onAnswer={onAnswer} />
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
        placeholder={number ? 'Year' : 'Your answer'}
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
