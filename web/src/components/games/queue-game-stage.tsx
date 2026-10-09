import { useQuery } from '@tanstack/react-query'
import { ArrowRight, Crown, Heart, Lightbulb, ListMusic, Swords, Trophy, Waypoints } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { UserAvatar } from '@/components/user-avatar'
import { useServerNow } from '@/hooks/use-server-now'
import { gameRoundQuery } from '@/lib/games'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo, spring } from '@/lib/motion'
import { playbackQuery } from '@/lib/playback'
import {
  champion,
  closeness,
  currentMatch,
  FIT_MARK,
  lastMatch,
  QUEUE_GAMES,
  queueGamesQuery,
  roundName,
  teamName,
  type GameEntry,
  type QueueGame,
} from '@/lib/queue-games'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

const ICONS = { connect: Waypoints, theme: ListMusic, bracket: Swords }

/**
 * Queue games on the big screen (MAD-794..796): the chain growing link by
 * link, a theme's entries and the hearts that decide it, and the bracket
 * between matches, with each winner moving on. A round that's up takes the
 * screen; the games wait as a pill until it's done.
 */
export function QueueGameStage({ roomId }: { roomId: string }) {
  const games = useQuery(queueGamesQuery(roomId)).data ?? []
  const round = useQuery(gameRoundQuery(roomId)).data
  const busy = !!round && round.mode !== 'ambient'
  return (
    <div className="pointer-events-none fixed inset-x-[6vw] bottom-[5vh] z-[54] flex flex-col gap-[2vh]">
      <AnimatePresence>
        {games.map((g, i) =>
          busy ? (
            <Pill key={g.id} game={g} />
          ) : (
            // With two up, the bracket keeps to a line.
            <Panel key={g.id} roomId={roomId} game={g} compact={games.length > 1 && g.kind === 'bracket' && i > 0} />
          ),
        )}
      </AnimatePresence>
    </div>
  )
}

function Pill({ game }: { game: QueueGame }) {
  const Icon = ICONS[game.kind]
  return (
    <motion.div
      layout
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="glass fixed top-[11vh] left-[3vw] flex items-center gap-[0.6vw] rounded-full px-[1.2vw] py-[0.8vh] text-[clamp(0.85rem,1.2vw,1.3rem)] font-semibold"
    >
      <Icon className="size-[2.6vh] text-primary" /> {QUEUE_GAMES[game.kind].label}
    </motion.div>
  )
}

function Panel({ roomId, game, compact }: { roomId: string; game: QueueGame; compact: boolean }) {
  return (
    <motion.div
      layout
      initial={{ opacity: 0, y: 60, scale: 0.97 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: 40 }}
      transition={{ duration: 0.8, ease: easeOutExpo }}
      role="status"
      aria-live="polite"
      className={cn('glass-strong flex flex-col shadow-float', compact ? 'gap-[1vh] rounded-[3vh] p-[2vh]' : 'gap-[2.2vh] rounded-[4vh] p-[3.5vh]')}
    >
      <Header game={game} />
      {!compact && game.kind === 'connect' && <ConnectBoard game={game} />}
      {!compact && game.kind === 'theme' && <ThemeBoard roomId={roomId} game={game} />}
      {game.kind === 'bracket' && <BracketBoard game={game} compact={compact} />}
    </motion.div>
  )
}

function Header({ game }: { game: QueueGame }) {
  const now = useServerNow(1000)
  const Icon = ICONS[game.kind]
  const left = Math.max(0, Math.ceil((Date.parse(game.closesAt) - now) / 1000))
  return (
    <div className="flex items-center gap-[1vw]">
      <Icon className="size-[3vh] text-primary" />
      <span className="text-[clamp(0.9rem,1.3vw,1.4rem)] font-semibold tracking-[0.18em] text-muted-foreground uppercase">
        {QUEUE_GAMES[game.kind].label}
      </span>
      <span className="ml-auto text-[clamp(1rem,1.6vw,1.7rem)] font-semibold tabular-nums">
        {game.state === 'open' && (
          <span className={cn(left <= 15 && 'text-destructive')}>
            {game.kind === 'connect' ? '' : 'Entries close in '}
            {Math.floor(left / 60)}:{String(left % 60).padStart(2, '0')}
          </span>
        )}
        {game.state === 'playing' && game.kind === 'theme' && <span className="text-muted-foreground">Heart your favourite</span>}
      </span>
    </div>
  )
}

function useUser() {
  const users = useQuery(usersQuery).data
  return (id: string) => users?.find((u) => u.id === id)
}

// --- Connect the artists ---------------------------------------------------------

function ConnectBoard({ game }: { game: QueueGame }) {
  const c = game.connect!
  const user = useUser()
  const over = game.state === 'reveal' || game.state === 'done'
  const miss = c.misses.at(-1)
  return (
    <>
      <p className="flex items-center gap-[1.2vw] text-[clamp(1.6rem,3.2vw,3.6rem)] leading-tight font-bold">
        {c.from} <ArrowRight className="size-[5vh] text-primary" /> {c.to}
      </p>
      {c.chains.map((ch, t) => (
        <div key={t} className="flex flex-col gap-[0.8vh]">
          {c.chains.length > 1 && (
            <p className="text-[clamp(0.85rem,1.2vw,1.3rem)] font-semibold text-muted-foreground">
              {teamName(t)}
              {over && c.winner === t && <Crown className="ml-[0.4vw] inline size-[2.6vh] text-primary" />}
            </p>
          )}
          <ol className="flex flex-wrap items-center gap-[0.6vw]">
            <Node name={c.from} end />
            {ch.links.map((l) => (
              <motion.li key={l.itemId} layout initial={{ opacity: 0, scale: 0.6 }} animate={{ opacity: 1, scale: 1 }} transition={spring} className="flex items-center gap-[0.6vw]">
                <span className="flex flex-col items-center text-[clamp(0.7rem,0.95vw,1rem)] text-muted-foreground">
                  <ArrowRight className="size-[2.6vh]" />
                  {closeness(l.score)}
                </span>
                <Node name={l.artist} by={user(l.userId)} end={ch.done && l.artist === ch.links.at(-1)?.artist} />
              </motion.li>
            ))}
            {!ch.done && (
              <li className="flex items-center gap-[0.6vw] text-muted-foreground">
                <ArrowRight className="size-[2.6vh]" /> <span className="text-[clamp(1rem,1.5vw,1.6rem)]">…</span> <ArrowRight className="size-[2.6vh]" />
                <Node name={c.to} faded />
              </li>
            )}
          </ol>
          {!over && ch.hints.length > 0 && (
            <p className="flex items-center gap-[0.5vw] text-[clamp(0.85rem,1.2vw,1.3rem)] text-primary">
              <Lightbulb className="size-[2.4vh]" /> Hint: {ch.hints.join(', then ')}
            </p>
          )}
        </div>
      ))}
      {over ? (
        <p className="text-[clamp(1rem,1.5vw,1.6rem)] text-muted-foreground">
          {c.winner === undefined ? 'Nobody got there. ' : ''}
          {c.path && <>The shortest way: {c.path.join(' → ')}</>}
        </p>
      ) : (
        <p className="text-[clamp(0.95rem,1.4vw,1.5rem)] text-muted-foreground">
          {miss
            ? `${miss.artist} didn’t link to ${miss.after}: ${miss.reason}.`
            : `Queue a song by an artist like the last one. About ${c.hops} songs gets there.`}
        </p>
      )}
    </>
  )
}

function Node({ name, by, end, faded }: { name: string; by?: ReturnType<ReturnType<typeof useUser>>; end?: boolean; faded?: boolean }) {
  return (
    <span
      style={laneStyle(by?.color)}
      className={cn(
        'flex items-center gap-[0.5vw] rounded-full px-[1vw] py-[0.6vh] text-[clamp(1rem,1.6vw,1.8rem)] font-semibold',
        end ? 'bg-primary/20 text-primary' : 'bg-foreground/8',
        faded && 'opacity-50',
      )}
    >
      {by && <UserAvatar user={by} className="size-[3vh] text-[1.1vh]" />}
      {name}
    </span>
  )
}

// --- Theme rounds ----------------------------------------------------------------

function ThemeBoard({ roomId, game }: { roomId: string; game: QueueGame }) {
  const playing = useQuery(playbackQuery(roomId)).data?.item?.id
  const over = game.state === 'reveal' || game.state === 'done'
  return (
    <>
      <p className="text-[clamp(1.6rem,3.2vw,3.6rem)] leading-tight font-bold text-balance">{game.theme?.prompt}</p>
      {game.entries.length === 0 ? (
        <p className="text-[clamp(1rem,1.5vw,1.6rem)] text-muted-foreground">Queue a song for it: the first one you add counts.</p>
      ) : (
        <ul className="grid grid-cols-2 gap-[1vw]">
          <AnimatePresence initial={false}>
            {game.entries.map((e, i) => (
              <EntryCard key={e.itemId} entry={e} playing={e.itemId === playing} won={over && (game.winners ?? []).includes(i)} over={over} />
            ))}
          </AnimatePresence>
        </ul>
      )}
    </>
  )
}

function EntryCard({ entry, playing, won, over }: { entry: GameEntry; playing: boolean; won: boolean; over: boolean }) {
  const user = useUser()(entry.userId)
  return (
    <motion.li
      layout
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: over && !won ? 0.5 : 1, y: 0, scale: won ? 1.03 : 1 }}
      transition={spring}
      style={laneStyle(user?.color)}
      className={cn(
        'flex items-center gap-[0.8vw] rounded-[2vh] border-2 border-transparent bg-foreground/8 p-[1.2vh]',
        playing && 'border-primary bg-primary/12',
        won && 'border-success bg-success/15',
      )}
    >
      {user && <UserAvatar user={user} className="size-[4.5vh] shrink-0 text-[1.6vh]" />}
      <div className="min-w-0 flex-1">
        <p className="truncate text-[clamp(1rem,1.5vw,1.6rem)] font-semibold">{entry.title}</p>
        <p className="truncate text-[clamp(0.8rem,1.1vw,1.15rem)] text-muted-foreground">
          {entry.fit && `${FIT_MARK[entry.fit]} `}
          {entry.note ?? entry.artist}
        </p>
      </div>
      {over && (
        <span className="flex shrink-0 items-center gap-[0.3vw] text-[clamp(1rem,1.5vw,1.6rem)] font-bold tabular-nums">
          {won && <Crown className="size-[3vh] text-success" />}
          {entry.hearts ?? 0} <Heart className="size-[2.6vh] text-primary" />
        </span>
      )}
    </motion.li>
  )
}

// --- Bracket battles -------------------------------------------------------------

function BracketBoard({ game, compact }: { game: QueueGame; compact: boolean }) {
  const b = game.bracket!
  const user = useUser()
  const match = currentMatch(game)
  const last = lastMatch(game)
  const champ = champion(game)
  if (game.state === 'open') {
    return (
      <>
        <p className={cn('font-bold', compact ? 'text-[clamp(1rem,1.5vw,1.6rem)]' : 'text-[clamp(1.6rem,3vw,3.2rem)]')}>
          {game.theme ? game.theme.prompt : 'Enter your best song'}
          <span className="ml-[1vw] font-semibold text-muted-foreground tabular-nums">
            {game.entries.length}/{b.size}
          </span>
        </p>
        {!compact && (
          <ul className="flex flex-wrap gap-[0.8vw]">
            {game.entries.map((e) => (
              <motion.li key={e.itemId} layout initial={{ scale: 0.6, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} transition={spring}>
                <Seed entry={e} />
              </motion.li>
            ))}
          </ul>
        )}
      </>
    )
  }
  if (match) {
    return (
      <div className="flex items-center gap-[1.5vw]">
        <span className="shrink-0 text-[clamp(0.85rem,1.2vw,1.3rem)] font-semibold tracking-[0.18em] text-primary uppercase">{match.name}</span>
        <Seed entry={match.a} big={!compact} />
        <span className="text-[clamp(1rem,1.6vw,1.7rem)] font-bold text-muted-foreground">vs</span>
        <Seed entry={match.b} big={!compact} />
        {!compact && (
          <span className="ml-auto flex items-center gap-[0.4vw] text-[clamp(0.9rem,1.3vw,1.4rem)] text-muted-foreground">
            <Heart className="size-[2.6vh] text-primary" /> Heart your favourite
          </span>
        )}
      </div>
    )
  }
  if (compact) {
    return (
      <p className="text-[clamp(0.9rem,1.3vw,1.4rem)] text-muted-foreground">
        {last ? `${last.winner.title} goes through.` : 'The first match is coming up.'}
      </p>
    )
  }
  return (
    <>
      {champ && (
        <motion.p
          initial={{ opacity: 0, scale: 0.9 }}
          animate={{ opacity: 1, scale: 1 }}
          transition={{ duration: 0.8, ease: easeOutExpo }}
          className="flex items-center gap-[1vw] text-[clamp(1.4rem,2.6vw,2.8rem)] font-bold"
        >
          <Trophy className="size-[5vh] text-primary" /> {champ.title}
          <span className="font-semibold text-muted-foreground">· {user(champ.userId)?.displayName ?? 'Someone'}</span>
        </motion.p>
      )}
      <div className="grid gap-[1.5vw]" style={{ gridTemplateColumns: `repeat(${b.rounds.length}, minmax(0, 1fr))` }}>
        {b.rounds.map((r, ri) => (
          <div key={ri} className="flex flex-col justify-around gap-[1vh]">
            <p className="text-[clamp(0.75rem,1vw,1.05rem)] font-semibold tracking-[0.18em] text-muted-foreground uppercase">
              {roundName(ri, b.rounds.length)}
            </p>
            {r.map((m, mi) => {
              const fresh = last && last.round === ri && last.index === mi
              return (
                <motion.div
                  key={mi}
                  layout
                  animate={fresh ? { scale: [1, 1.05, 1] } : { scale: 1 }}
                  transition={{ duration: 1.2, ease: easeOutExpo }}
                  className={cn('flex flex-col gap-[0.4vh] rounded-[1.6vh] bg-foreground/6 p-[0.8vh]', fresh && 'ring-[0.3vh] ring-success')}
                >
                  {[m.a, m.b].map((side, k) => (
                    <Slot key={k} entry={side >= 0 ? game.entries[side] : undefined} bye={m.bye && k === 1} won={m.winner >= 0 && m.winner === side} lost={m.winner >= 0 && m.winner !== side} />
                  ))}
                </motion.div>
              )
            })}
          </div>
        ))}
      </div>
    </>
  )
}

function Seed({ entry, big }: { entry: GameEntry; big?: boolean }) {
  const user = useUser()(entry.userId)
  return (
    <span style={laneStyle(user?.color)} className="flex min-w-0 items-center gap-[0.5vw] rounded-full bg-foreground/8 py-[0.5vh] pr-[1vw] pl-[0.5vh]">
      {user && <UserAvatar user={user} className={cn(big ? 'size-[4.5vh] text-[1.6vh]' : 'size-[3vh] text-[1.1vh]')} />}
      <span className={cn('truncate font-semibold', big ? 'text-[clamp(1.1rem,1.8vw,2rem)]' : 'text-[clamp(0.85rem,1.2vw,1.3rem)]')}>{entry.title}</span>
    </span>
  )
}

function Slot({ entry, bye, won, lost }: { entry?: GameEntry; bye: boolean; won: boolean; lost: boolean }) {
  const user = useUser()(entry?.userId ?? '')
  return (
    <span
      style={laneStyle(user?.color)}
      className={cn(
        'flex min-h-[3.4vh] min-w-0 items-center gap-[0.4vw] text-[clamp(0.75rem,1vw,1.1rem)]',
        won && 'font-bold text-(--lane)',
        lost && 'opacity-40',
        !entry && 'text-muted-foreground',
      )}
    >
      {user && <UserAvatar user={user} className="size-[2.6vh] shrink-0 text-[1vh]" />}
      <span className="truncate">{entry ? entry.title : bye ? 'Bye' : '—'}</span>
    </span>
  )
}
