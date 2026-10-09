import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowRight, Crown, Heart, Lightbulb, ListMusic, LoaderCircle, Search, Swords, Trophy, Waypoints, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState, type ReactNode } from 'react'
import { errorMessage } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { useMe } from '@/lib/auth'
import { gameRoundQuery, useGamesMuted } from '@/lib/games'
import { tap } from '@/lib/haptics'
import { easeOutExpo } from '@/lib/motion'
import type { QueueItem } from '@/lib/playback'
import {
  chainFor,
  champion,
  closeQueueGame,
  currentMatch,
  enterable,
  enterQueueGame,
  entriesLeft,
  FIT_MARK,
  hintQueueGame,
  lastMatch,
  mayClose,
  myEntries,
  QUEUE_GAMES,
  queueGamesQuery,
  teamName,
  withdrawEntry,
  type GameEntry,
  type QueueGame,
} from '@/lib/queue-games'
import { queueQuery, useCurrentRoom } from '@/lib/room'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'
import { useServerNow } from '@/hooks/use-server-now'

const ICONS = { connect: Waypoints, theme: ListMusic, bracket: Swords }

/**
 * The room's queue games on a phone (MAD-794..796): a card for each, over
 * now playing, folded to a chip when you like. They're played through the
 * queue, so most of what you do is queue songs; the card says what the
 * game wants, and takes your entries. A round that's up goes first.
 */
export function QueueGameDock() {
  const { room } = useCurrentRoom()
  const games = useQuery({ ...queueGamesQuery(room?.id ?? ''), enabled: !!room }).data ?? []
  const round = useQuery({ ...gameRoundQuery(room?.id ?? ''), enabled: !!room }).data
  const muted = useGamesMuted()
  const [folded, setFolded] = useState<Record<string, boolean>>({})
  if (!room || games.length === 0 || round) return null
  // A game unfolds again when it moves on: the block's playing, the result's in.
  const key = (g: QueueGame) => `${g.id}:${g.state}:${g.bracket?.current ? 'match' : ''}`
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-[calc(var(--spacing-nav)+var(--spacing-mini)+env(safe-area-inset-bottom)+1.5rem)] z-[54] flex flex-col items-center gap-2 px-3">
      <AnimatePresence initial={false}>
        {games.map((g) =>
          (folded[key(g)] ?? muted) ? (
            <Chip key={`chip-${g.id}`} game={g} onOpen={() => setFolded((f) => ({ ...f, [key(g)]: false }))} />
          ) : (
            <Card key={g.id} roomId={room.id} ownerId={room.ownerId} game={g} onFold={() => setFolded((f) => ({ ...f, [key(g)]: true }))} />
          ),
        )}
      </AnimatePresence>
    </div>
  )
}

function Chip({ game, onOpen }: { game: QueueGame; onOpen: () => void }) {
  const Icon = ICONS[game.kind]
  return (
    <motion.button
      type="button"
      layout
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: 12 }}
      transition={{ duration: 0.3, ease: easeOutExpo }}
      onClick={onOpen}
      className="glass-strong pointer-events-auto flex max-w-2xl items-center gap-2 rounded-full py-2 pr-4 pl-3 text-sm shadow-float outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <Icon className="size-4 text-primary" />
      <span className="font-medium">{QUEUE_GAMES[game.kind].label}</span>
      <span className="text-muted-foreground">· {status(game)}</span>
    </motion.button>
  )
}

/** Where a game is, in a few words. */
function status(game: QueueGame) {
  if (game.state === 'reveal' || game.state === 'done') return 'See how it went'
  if (game.kind === 'connect') return 'Queue the next link'
  if (game.state === 'open') return `${game.entries.length} in`
  if (game.kind === 'theme') return 'Heart your favourite'
  return currentMatch(game) ? 'A match is on' : 'Between matches'
}

function Card({ roomId, ownerId, game, onFold }: { roomId: string; ownerId: string; game: QueueGame; onFold: () => void }) {
  const me = useMe()
  const now = useServerNow(1000)
  const Icon = ICONS[game.kind]
  const close = useMutation({
    mutationFn: () => closeQueueGame(roomId, game.id),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const left = Math.max(0, Math.ceil((Date.parse(game.closesAt) - now) / 1000))
  const canPlay = !me.guest || game.guests
  return (
    <motion.section
      layout
      initial={{ opacity: 0, y: 40 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: 40 }}
      transition={{ duration: 0.4, ease: easeOutExpo }}
      aria-label={QUEUE_GAMES[game.kind].label}
      className="glass-strong pointer-events-auto flex w-full max-w-md flex-col gap-3 rounded-3xl p-4 shadow-float"
    >
      <header className="flex items-center gap-2">
        <Icon className="size-4 text-primary" />
        <span className="text-caption font-semibold tracking-wide text-muted-foreground uppercase">{QUEUE_GAMES[game.kind].label}</span>
        {game.state === 'open' && (
          <span className={cn('ml-auto text-sm font-semibold tabular-nums', left <= 15 && 'text-destructive')}>{clock(left)}</span>
        )}
        <Button size="icon-sm" variant="ghost" aria-label="Fold the game away" onClick={onFold} className={cn(game.state !== 'open' && 'ml-auto')}>
          <X />
        </Button>
      </header>

      {game.kind === 'connect' && <Connect roomId={roomId} game={game} canPlay={canPlay} />}
      {game.kind === 'theme' && <Theme roomId={roomId} game={game} canPlay={canPlay} />}
      {game.kind === 'bracket' && <Bracket roomId={roomId} game={game} canPlay={canPlay} />}

      {mayClose(game, me, ownerId) && (game.state === 'open' || game.state === 'playing') && (
        <Button size="sm" variant="ghost" className="self-end text-muted-foreground" disabled={close.isPending} onClick={() => close.mutate()}>
          {closeLabel(game)}
        </Button>
      )}
    </motion.section>
  )
}

function closeLabel(game: QueueGame) {
  if (game.kind === 'connect') return 'End it now'
  if (game.state === 'open') return 'Close entries now'
  return game.kind === 'theme' ? 'Count the hearts now' : 'End the bracket'
}

function clock(seconds: number) {
  const m = Math.floor(seconds / 60)
  return `${m}:${String(seconds % 60).padStart(2, '0')}`
}

function useName() {
  const me = useMe()
  const users = useQuery(usersQuery).data
  return (id: string) => (id === me.id ? 'You' : (users?.find((u) => u.id === id)?.displayName ?? 'Someone'))
}

// --- Connect the artists ---------------------------------------------------------

function Connect({ roomId, game, canPlay }: { roomId: string; game: QueueGame; canPlay: boolean }) {
  const me = useMe()
  const c = game.connect!
  const mine = chainFor(game, me.id)
  const hint = useMutation({
    mutationFn: () => hintQueueGame(roomId, game.id),
    onMutate: () => tap(),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const over = game.state === 'reveal' || game.state === 'done'
  const miss = c.misses.findLast((m) => m.userId === me.id)
  const points = game.points.find((p) => p.userId === me.id)?.points ?? 0
  return (
    <>
      <p className="flex flex-wrap items-center gap-1.5 text-base font-semibold">
        {c.from} <ArrowRight className="size-4 text-muted-foreground" /> {c.to}
      </p>
      {mine?.chain ? (
        <Chain from={c.from} to={c.to} artists={mine.chain.links.map((l) => l.artist)} done={mine.chain.done} />
      ) : (
        c.chains.length > 1 && <p className="text-caption text-muted-foreground">You’ll join a team with your first song.</p>
      )}
      {!over ? (
        <>
          <p className="text-sm text-muted-foreground">
            {mine?.chain?.done
              ? 'Your chain’s there! Waiting on the other team.'
              : `Queue a song by an artist like ${mine?.last ?? c.from}. About ${c.hops} songs gets there.`}
            {mine?.team !== undefined && c.chains.length > 1 && <span className="font-medium text-foreground"> · {teamName(mine.team)}</span>}
          </p>
          {mine?.chain && mine.chain.hints.length > 0 && (
            <p className="flex items-center gap-1.5 text-caption text-primary">
              <Lightbulb className="size-3.5" /> Try {mine.chain.hints.join(', then ')}
            </p>
          )}
          {miss && (
            <p className="text-caption text-muted-foreground">
              {miss.artist} didn’t link to {miss.after}: {miss.reason}.
            </p>
          )}
          {canPlay && (
            <div className="flex gap-2">
              <Button asChild size="sm" className="flex-1">
                <Link to="/search">
                  <Search data-icon="inline-start" /> Find a song
                </Link>
              </Button>
              <Button size="sm" variant="secondary" disabled={hint.isPending || !!mine?.chain?.done} onClick={() => hint.mutate()}>
                <Lightbulb data-icon="inline-start" /> Hint
              </Button>
            </div>
          )}
        </>
      ) : (
        <Result>
          <p className="text-sm font-semibold">
            {c.winner !== undefined
              ? c.chains.length > 1
                ? `${teamName(c.winner)} got there first, in ${c.chains[c.winner].links.length}!`
                : `Connected in ${c.chains[0].links.length}!`
              : 'Nobody got there this time.'}
          </p>
          {c.path && <p className="text-caption text-muted-foreground">The shortest way: {c.path.join(' → ')}</p>}
          {points > 0 && <p className="text-caption text-success">+{points.toLocaleString()} for you</p>}
          {game.scores === 'board' && <Points game={game} />}
        </Result>
      )}
    </>
  )
}

/** A chain of artists, from one end towards the other. */
function Chain({ from, to, artists, done }: { from: string; to: string; artists: string[]; done: boolean }) {
  const all = [from, ...artists]
  return (
    <ol className="flex flex-wrap items-center gap-1 text-caption">
      {all.map((a, i) => (
        <li key={`${a}-${i}`} className="flex items-center gap-1">
          {i > 0 && <ArrowRight className="size-3 text-muted-foreground" />}
          <motion.span
            initial={{ scale: 0.6, opacity: 0 }}
            animate={{ scale: 1, opacity: 1 }}
            className={cn('rounded-full px-2 py-0.5 font-medium', i === 0 || (done && i === all.length - 1) ? 'bg-primary/15 text-primary' : 'bg-muted')}
          >
            {a}
          </motion.span>
        </li>
      ))}
      {!done && (
        <li className="flex items-center gap-1 text-muted-foreground">
          <ArrowRight className="size-3" /> … <ArrowRight className="size-3" /> {to}
        </li>
      )}
    </ol>
  )
}

// --- Theme rounds ----------------------------------------------------------------

function Theme({ roomId, game, canPlay }: { roomId: string; game: QueueGame; canPlay: boolean }) {
  const me = useMe()
  const name = useName()
  const over = game.state === 'reveal' || game.state === 'done'
  const mine = myEntries(game, me.id)[0]
  return (
    <>
      <p className="text-base leading-snug font-semibold text-balance">{game.theme?.prompt}</p>
      {game.state === 'open' && canPlay && (
        <>
          {mine ? <EntryLine entry={mine} /> : <p className="text-sm text-muted-foreground">Queue a song for it: the next one you add counts.</p>}
          <Enter roomId={roomId} game={game} swap={!!mine} />
        </>
      )}
      {game.state === 'playing' && (
        <>
          <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
            <Heart className="size-3.5 text-primary" /> The entries play now, one after another. Heart your favourite.
          </p>
          <Entries entries={game.entries} />
        </>
      )}
      {over && (
        <Result>
          {(game.winners ?? []).length === 0 ? (
            <p className="text-sm font-semibold">No hearts, no winner. Tough crowd.</p>
          ) : (
            (game.winners ?? []).map((i) => (
              <p key={i} className="flex items-center gap-1.5 text-sm font-semibold">
                <Crown className="size-4 text-primary" /> {game.entries[i].title}
                <span className="font-normal text-muted-foreground">
                  · {name(game.entries[i].userId)} · {game.entries[i].hearts} ♥
                </span>
              </p>
            ))
          )}
          <Points game={game} />
        </Result>
      )}
    </>
  )
}

function EntryLine({ entry }: { entry: GameEntry }) {
  return (
    <p className="text-sm">
      Your entry: <span className="font-semibold">{entry.title}</span>
      {entry.fit && (
        <span className={cn('ml-1.5 text-caption', entry.fit === 'yes' ? 'text-success' : 'text-muted-foreground')}>
          {FIT_MARK[entry.fit]} {entry.note ?? (entry.fit === 'yes' ? 'Fits' : '')}
        </span>
      )}
    </p>
  )
}

function Entries({ entries }: { entries: GameEntry[] }) {
  const name = useName()
  return (
    <ol className="flex flex-col gap-1 text-caption">
      {entries.map((e) => (
        <li key={e.itemId} className={cn('flex items-center gap-2', e.played && 'text-muted-foreground')}>
          <span className="truncate font-medium">{e.title}</span>
          <span className="shrink-0 text-muted-foreground">· {name(e.userId)}</span>
          {e.fit && <span className="ml-auto shrink-0">{FIT_MARK[e.fit]}</span>}
        </li>
      ))}
    </ol>
  )
}

/** Your songs waiting in the queue, to enter: tap one. */
function Enter({ roomId, game, swap }: { roomId: string; game: QueueGame; swap: boolean }) {
  const me = useMe()
  const queue = useQuery(queueQuery(roomId)).data
  const games = useQuery(queueGamesQuery(roomId)).data ?? []
  const enter = useMutation({
    mutationFn: (item: QueueItem) => enterQueueGame(roomId, game.id, item.id),
    onMutate: () => tap(),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const songs = enterable(queue?.items ?? [], games, me.id).slice(0, 4)
  if (!swap && entriesLeft(game, me.id) === 0) return null
  return (
    <div className="flex flex-col gap-1.5">
      {songs.length > 0 && (
        <p className="text-caption text-muted-foreground">{swap ? 'Or swap in one of yours:' : 'Or enter one you queued:'}</p>
      )}
      {songs.map((s) => (
        <button
          key={s.id}
          type="button"
          disabled={enter.isPending}
          onClick={() => enter.mutate(s)}
          className="flex items-center gap-2 rounded-xl bg-muted/60 px-3 py-2 text-left text-sm transition-colors outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <span className="min-w-0 flex-1 truncate">
            <span className="font-medium">{s.track.title}</span>
            <span className="text-muted-foreground"> · {s.track.artists.join(', ')}</span>
          </span>
          {enter.isPending && enter.variables?.id === s.id ? <LoaderCircle className="size-4 animate-spin" /> : <span className="text-caption text-primary">Enter</span>}
        </button>
      ))}
      <Button asChild size="sm" variant="secondary">
        <Link to="/search">
          <Search data-icon="inline-start" /> Find a song
        </Link>
      </Button>
    </div>
  )
}

// --- Bracket battles -------------------------------------------------------------

function Bracket({ roomId, game, canPlay }: { roomId: string; game: QueueGame; canPlay: boolean }) {
  const me = useMe()
  const name = useName()
  const b = game.bracket!
  const mine = myEntries(game, me.id)
  const withdraw = useMutation({
    mutationFn: (itemId: string) => withdrawEntry(roomId, game.id, itemId),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const match = currentMatch(game)
  const last = lastMatch(game)
  const champ = champion(game)
  const over = game.state === 'reveal' || game.state === 'done'
  return (
    <>
      {game.theme && <p className="text-sm font-semibold">Theme: {game.theme.prompt}</p>}
      {game.state === 'open' && (
        <>
          <p className="text-sm text-muted-foreground">
            {game.entries.length} of {b.size} songs in{b.short ? ' · short versions' : ''}. Up to two each.
          </p>
          {mine.map((e) => (
            <div key={e.itemId} className="flex items-center gap-2">
              <EntryLine entry={e} />
              <Button size="icon-sm" variant="ghost" aria-label={`Take ${e.title} out`} onClick={() => withdraw.mutate(e.itemId)}>
                <X />
              </Button>
            </div>
          ))}
          {canPlay && entriesLeft(game, me.id) > 0 && (
            <>
              {mine.length === 0 && <p className="text-sm text-muted-foreground">Queue a song to enter it: the next one you add counts.</p>}
              <Enter roomId={roomId} game={game} swap={false} />
            </>
          )}
        </>
      )}
      {game.state === 'playing' &&
        (match ? (
          <div className="flex flex-col gap-1.5 rounded-2xl bg-primary/10 px-3 py-2.5">
            <p className="text-caption font-semibold tracking-wide text-primary uppercase">{match.name}</p>
            <p className="text-sm">
              <span className="font-semibold">{match.a.title}</span> <span className="text-muted-foreground">({name(match.a.userId)})</span>
              <span className="mx-1.5 text-muted-foreground">vs</span>
              <span className="font-semibold">{match.b.title}</span> <span className="text-muted-foreground">({name(match.b.userId)})</span>
            </p>
            <p className="flex items-center gap-1.5 text-caption text-muted-foreground">
              <Heart className="size-3.5 text-primary" /> Back to back. Heart the one you love.
            </p>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            {last
              ? `${last.winner.title} won the ${last.name.toLowerCase()} match. The next one comes up between songs.`
              : 'The first match comes up between songs.'}
          </p>
        ))}
      {game.state === 'playing' && mine.length > 0 && (
        <p className="text-caption text-muted-foreground">{bracketStanding(game, me.id)}</p>
      )}
      {over && (
        <Result>
          {champ ? (
            <p className="flex items-center gap-1.5 text-sm font-semibold">
              <Trophy className="size-4 text-primary" /> {champ.title} won the bracket
              <span className="font-normal text-muted-foreground">· {name(champ.userId)}</span>
            </p>
          ) : (
            <p className="text-sm font-semibold">The bracket ended early.</p>
          )}
          <Points game={game} />
        </Result>
      )}
    </>
  )
}

/** How your songs are doing in the bracket. */
function bracketStanding(game: QueueGame, userId: string) {
  const b = game.bracket!
  const alive = game.entries
    .map((e, i) => ({ e, i }))
    .filter(({ e }) => e.userId === userId)
    .filter(({ i }) => !b.rounds.some((r) => r.some((m) => m.state === 'done' && (m.a === i || m.b === i) && m.winner !== i)))
  if (alive.length === 0) return 'You’re out. Cheer on the rest.'
  return `Still in: ${alive.map(({ e }) => e.title).join(', ')}`
}

function Points({ game }: { game: QueueGame }) {
  const me = useMe()
  const name = useName()
  // Your own only, when the room keeps scores private.
  const top = game.points.filter((p) => p.points > 0 && (game.scores !== 'private' || p.userId === me.id)).slice(0, 3)
  if (top.length === 0 || game.scores === 'off') return null
  return (
    <p className="text-caption text-muted-foreground tabular-nums">
      {top.map((p) => `${name(p.userId)} +${p.points.toLocaleString()}`).join(' · ')}
    </p>
  )
}

function Result({ children }: { children: ReactNode }) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.5, ease: easeOutExpo }}
      className="flex flex-col gap-1 rounded-2xl bg-muted/60 px-3 py-2.5"
    >
      {children}
    </motion.div>
  )
}
