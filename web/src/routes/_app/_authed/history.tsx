import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { ChevronLeft, Crown, ListMusic, LoaderCircle, Sparkles } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState, type ReactNode } from 'react'
import { StatsView } from '@/components/history/stats-view'
import { PageHeader } from '@/components/page-header'
import { SaveNightButton } from '@/components/playlists/save-night'
import { SaveToPlaylistButton } from '@/components/playlists/save-to-playlist'
import { QueueRow } from '@/components/room/queue-row'
import { RequeueButton } from '@/components/room/requeue-button'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { WrappedStory } from '@/components/wrapped/wrapped-story'
import { useMe } from '@/lib/auth'
import {
  formatListening,
  historyPagesQuery,
  sessionName,
  sessionRange,
  sessionsQuery,
  statsQuery,
  type ListeningSession,
  type PlayedItem,
} from '@/lib/history'
import { isGuest } from '@/lib/guests'
import { spring } from '@/lib/motion'
import { recapQuery } from '@/lib/playlists'
import type { User } from '@/lib/now-playing'
import { useCurrentRoom, type Room } from '@/lib/room'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

export const Route = createFileRoute('/_app/_authed/history')({
  component: History,
})

type Tab = 'played' | 'recaps' | 'all'

/** What the room played: the full history, recaps of each session, and all-time stats. */
function History() {
  const { room, rooms } = useCurrentRoom()
  const [tab, setTab] = useState<Tab>('played')

  return (
    <>
      <div className="pt-6">
        <Button asChild variant="glass" size="icon" aria-label="Back to the room">
          <Link to="/room">
            <ChevronLeft className="size-5" />
          </Link>
        </Button>
      </div>
      <PageHeader title="History" subtitle={room?.name} />
      {rooms.isPending ? (
        <Skeleton className="h-64 rounded-3xl" />
      ) : !room ? (
        <p className="glass rounded-3xl p-5 text-sm text-muted-foreground">
          Join a room to see what it played. <Link to="/room" className="text-primary">Pick a room</Link>
        </p>
      ) : (
        <div className="flex flex-col gap-5">
          <ToggleGroup type="single" value={tab} onValueChange={(v) => v && setTab(v as Tab)} aria-label="View" className="self-start">
            <ToggleGroupItem value="played">Played</ToggleGroupItem>
            <ToggleGroupItem value="recaps">Recaps</ToggleGroupItem>
            <ToggleGroupItem value="all">All time</ToggleGroupItem>
          </ToggleGroup>
          {tab === 'played' && <Played room={room} />}
          {tab === 'recaps' && <Recaps room={room} />}
          {tab === 'all' && <StatsView roomId={room.id} />}
        </div>
      )}
    </>
  )
}

/** Every song the room played, newest first, filtered to one person if you like. */
function Played({ room }: { room: Room }) {
  const me = useMe()
  const [person, setPerson] = useState<string>()
  const history = useInfiniteQuery(historyPagesQuery(room.id, person))
  // Whose songs have played, for the filter: everyone in the all-time stats.
  const stats = useQuery(statsQuery(room.id))
  const users = useQuery(usersQuery)
  const userById = (id: string) => users.data?.find((u) => u.id === id)
  const played = history.data?.pages.flat() ?? []

  return (
    <div className="flex flex-col gap-4">
      {(stats.data?.people.length ?? 0) > 1 && (
        <div className="-mx-gutter flex gap-2 overflow-x-auto px-gutter pb-1" role="group" aria-label="Whose songs">
          <Chip active={!person} onClick={() => setPerson(undefined)}>
            Everyone
          </Chip>
          {stats.data?.people.map((p) => {
            const u = userById(p.userId)
            return (
              <Chip key={p.userId} active={person === p.userId} onClick={() => setPerson(p.userId)}>
                {u && <UserAvatar user={u} className="size-5 text-[0.55rem]" />}
                {p.userId === me.id ? 'You' : (u?.displayName ?? 'Someone')}
              </Chip>
            )
          })}
        </div>
      )}

      {history.isPending ? (
        <Skeleton className="h-64 rounded-3xl" />
      ) : played.length === 0 ? (
        <p className="glass rounded-3xl p-5 text-sm text-muted-foreground">Nothing has played here yet.</p>
      ) : (
        byDay(played).map(([day, plays]) => (
          <section key={day}>
            <h2 className="mb-2 px-1 text-sm font-medium text-muted-foreground">{day}</h2>
            <ol className="glass flex flex-col rounded-3xl p-1.5">
              {plays.map((p) => (
                <li key={`${p.item.id}-${p.startedAt}`}>
                  <QueueRow
                    roomId={room.id}
                    item={p.item}
                    user={userById(p.item.addedBy)}
                    byline
                    className={cn(p.endReason !== 'finished' && 'opacity-75')}
                    trailing={
                      <>
                        <span className="shrink-0 text-caption text-muted-foreground tabular-nums">
                          {p.endReason === 'skipped'
                            ? 'Skipped'
                            : p.endReason === 'error'
                              ? "Didn't play"
                              : new Date(p.startedAt).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}
                        </span>
                        <SaveToPlaylistButton song={{ itemId: p.item.id }} title={p.item.track.title} className="hidden sm:grid" />
                        <RequeueButton item={p.item} />
                      </>
                    }
                  />
                </li>
              ))}
            </ol>
          </section>
        ))
      )}

      {history.hasNextPage && (
        <Button variant="glass" onClick={() => void history.fetchNextPage()} disabled={history.isFetchingNextPage} className="self-center">
          {history.isFetchingNextPage && <LoaderCircle className="animate-spin" />}
          Show more
        </Button>
      )}
    </div>
  )
}

/** Plays grouped under the day they started, in order. */
function byDay(plays: PlayedItem[]) {
  const groups = new Map<string, PlayedItem[]>()
  for (const p of plays) {
    const day = new Date(p.startedAt).toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric' })
    groups.set(day, [...(groups.get(day) ?? []), p])
  }
  return [...groups]
}

/** Each listening session, and the recap of the one you pick. */
function Recaps({ room }: { room: Room }) {
  const sessions = useQuery(sessionsQuery(room.id))
  const users = useQuery(usersQuery)
  const [picked, setPicked] = useState<string>()
  const list = sessions.data ?? []
  const current = list.find((s) => s.startedAt === picked) ?? list[0]

  if (sessions.isPending) return <Skeleton className="h-64 rounded-3xl" />
  if (!current) return <p className="glass rounded-3xl p-5 text-sm text-muted-foreground">No sessions yet. Play something!</p>

  return (
    <div className="flex flex-col gap-5">
      <ul className="-mx-gutter flex snap-x gap-2 overflow-x-auto px-gutter pb-1" aria-label="Sessions">
        {list.map((s) => (
          <li key={s.startedAt} className="snap-start">
            <SessionCard
              session={s}
              active={s === current}
              onClick={() => setPicked(s.startedAt)}
              people={s.people.map((id) => users.data?.find((u) => u.id === id)).filter((u): u is User => !!u)}
            />
          </li>
        ))}
      </ul>
      <AnimatePresence mode="wait" initial={false}>
        <motion.div key={current.startedAt} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -8 }} transition={spring}>
          <h2 className="mb-3 px-1 text-title">{sessionName(current)}</h2>
          <RecapActions room={room} session={current} />
          <StatsView roomId={room.id} range={sessionRange(current)} recap night={current.night} />
        </motion.div>
      </AnimatePresence>
    </div>
  )
}

/** Play the session's Wrapped, and save it as a playlist (or open the one saved). */
function RecapActions({ room, session }: { room: Room; session: ListeningSession }) {
  const me = useMe()
  const range = sessionRange(session)
  const from = range.from!
  const to = range.to!
  const recap = useQuery(recapQuery(room.id, from, to))
  const [playing, setPlaying] = useState(false)
  const saved = recap.data?.playlists[0]
  return (
    <div className="mb-5 flex flex-wrap gap-2">
      <Button onClick={() => setPlaying(true)}>
        <Sparkles data-icon="inline-start" />
        Play Wrapped
      </Button>
      {saved ? (
        <Button asChild variant="glass">
          <Link to="/library/$playlistId" params={{ playlistId: saved.id }}>
            <ListMusic data-icon="inline-start" />
            {saved.name}
          </Link>
        </Button>
      ) : (
        !isGuest(me) && <SaveNightButton night={{ roomId: room.id, roomName: room.name, from, to }} variant="glass" />
      )}
      <AnimatePresence>
        {playing && <WrappedStory roomId={room.id} roomName={room.name} from={from} to={to} onClose={() => setPlaying(false)} />}
      </AnimatePresence>
    </div>
  )
}

function SessionCard({
  session,
  active,
  onClick,
  people,
}: {
  session: ListeningSession
  active: boolean
  onClick: () => void
  people: User[]
}) {
  const start = new Date(session.startedAt)
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        'glass flex w-44 flex-col gap-2 rounded-3xl p-4 text-left transition-shadow outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
        active && 'ring-2 ring-primary',
      )}
    >
      <span className="flex items-center gap-1.5 text-sm font-medium">
        {sessionName(session)}
        {session.night?.songOfTheNight && <Crown className="size-3.5 text-amber-500 dark:text-amber-300" aria-label="Has a song of the night" />}
      </span>
      <span className="text-caption text-muted-foreground">
        {start.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} · {session.plays} songs ·{' '}
        {formatListening(Date.parse(session.endedAt) - start.getTime())}
      </span>
      <span className="flex -space-x-1.5">
        {people.slice(0, 5).map((u) => (
          <UserAvatar key={u.id} user={u} className="size-6 text-[0.55rem] ring-2 ring-background" />
        ))}
      </span>
    </button>
  )
}

function Chip({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        'flex shrink-0 items-center gap-1.5 rounded-full px-3 py-1.5 text-sm font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
        active ? 'bg-primary text-primary-foreground' : 'glass text-muted-foreground hover:text-foreground',
      )}
    >
      {children}
    </button>
  )
}
