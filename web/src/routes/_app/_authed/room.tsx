import { useQuery } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { Check, ChevronDown, Plus, Sparkles, Speaker } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { DropdownMenu } from 'radix-ui'
import { useState } from 'react'
import { Artwork } from '@/components/artwork'
import { PageHeader } from '@/components/page-header'
import { MyLane } from '@/components/room/my-lane'
import { QueueRow } from '@/components/room/queue-row'
import { SpeakerPanel } from '@/components/room/speaker-panel'
import { TransportControls } from '@/components/shell/player-controls'
import { StartRoom } from '@/components/start-room'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Slider } from '@/components/ui/slider'
import { UserAvatar } from '@/components/user-avatar'
import { usePosition } from '@/hooks/use-position'
import { useMe } from '@/lib/auth'
import { laneStyle } from '@/lib/lane'
import { fadeUp, spring, stagger } from '@/lib/motion'
import { formatDuration, usePlayer, type User } from '@/lib/now-playing'
import { playbackQuery, songsBeforeYours, type QueueItem } from '@/lib/playback'
import { chooseRoom, queueQuery, useCurrentRoom, type Room as RoomInfo } from '@/lib/room'
import { live } from '@/lib/room-socket'
import { useStore } from '@/lib/store'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

export const Route = createFileRoute('/_app/_authed/room')({
  component: Room,
})

// How much of the fair order to show before summarizing the rest.
const UP_NEXT_SHOWN = 12

function Room() {
  const me = useMe()
  const { room, rooms } = useCurrentRoom()
  const queue = useQuery({ ...queueQuery(room?.id ?? ''), enabled: !!room })
  const users = useQuery(usersQuery)
  const userById = (id: string) => users.data?.find((u) => u.id === id)

  if (rooms.isPending || (room && queue.isPending)) {
    return (
      <>
        <PageHeader title="Room" />
        <div className="flex flex-col gap-4">
          <Skeleton className="h-72 rounded-3xl" />
          <Skeleton className="h-40 rounded-3xl" />
        </div>
      </>
    )
  }
  if (!room) {
    return (
      <>
        <PageHeader title="Room" subtitle="Everyone takes turns. Add a song to your lane." />
        <StartRoom />
      </>
    )
  }

  const items = queue.data?.items ?? []
  const byId = new Map(items.map((i) => [i.id, i]))
  const upNext = (queue.data?.upNext ?? []).map((id) => byId.get(id)).filter((i): i is QueueItem => !!i)
  const mine = items.filter((i) => i.addedBy === me.id && i.state === 'queued').sort((a, b) => a.lanePosition - b.lanePosition)
  const before = songsBeforeYours(queue.data?.upNext ?? [], items, me.id)

  return (
    <>
      <RoomHeader room={room} rooms={rooms.data ?? []} />

      <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-6">
        <NowPlayingCard room={room} waiting={upNext.length} />

        {before !== undefined && (
          <motion.p
            variants={fadeUp}
            style={laneStyle(me.color)}
            className="flex items-center gap-2 rounded-2xl bg-(--lane)/12 px-4 py-3 text-sm font-medium"
          >
            <span className="size-2 rounded-full bg-(--lane) shadow-[0_0_8px_var(--lane)]" />
            {before === 0
              ? "You're up next"
              : `Your next song plays in ~${before} ${before === 1 ? 'turn' : 'turns'}`}
          </motion.p>
        )}

        <motion.section variants={fadeUp}>
          <SectionTitle title="Up next" count={upNext.length} />
          {upNext.length === 0 ? (
            <EmptyQueue />
          ) : (
            <ol className="glass flex flex-col rounded-3xl p-1.5">
              <AnimatePresence initial={false}>
                {upNext.slice(0, UP_NEXT_SHOWN).map((item, i) => (
                  <motion.li
                    key={item.id}
                    layout
                    initial={{ opacity: 0, scale: 0.97 }}
                    animate={{ opacity: 1, scale: 1 }}
                    exit={{ opacity: 0, scale: 0.97 }}
                    transition={spring}
                  >
                    <QueueRow
                      roomId={room.id}
                      item={item}
                      user={userById(item.addedBy)}
                      mine={item.addedBy === me.id}
                      leading={
                        <span className="w-5 shrink-0 text-center text-sm text-muted-foreground tabular-nums">{i + 1}</span>
                      }
                    />
                  </motion.li>
                ))}
              </AnimatePresence>
              {upNext.length > UP_NEXT_SHOWN && (
                <li className="px-4 py-2.5 text-sm text-muted-foreground">
                  and {upNext.length - UP_NEXT_SHOWN} more
                </li>
              )}
            </ol>
          )}
        </motion.section>

        <motion.section variants={fadeUp}>
          <SectionTitle title="Your lane" count={mine.length} hint={mine.length > 1 ? 'Drag to reorder · swipe to remove' : undefined} />
          <MyLane roomId={room.id} items={mine} me={me} />
        </motion.section>

        <Lanes items={items} me={me.id} userById={userById} />
      </motion.div>
    </>
  )
}

function RoomHeader({ room, rooms }: { room: RoomInfo; rooms: RoomInfo[] }) {
  const { members, status } = useStore(live)
  const title =
    rooms.length > 1 ? (
      <DropdownMenu.Root>
        <DropdownMenu.Trigger className="-ml-1 flex items-center gap-1.5 rounded-xl px-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
          <span className="truncate">{room.name}</span>
          <ChevronDown className="size-6 shrink-0 text-muted-foreground" />
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="start"
            sideOffset={8}
            className="glass-strong z-50 min-w-56 rounded-2xl p-1.5 shadow-float data-[state=open]:animate-in data-[state=open]:fade-in data-[state=open]:zoom-in-95"
          >
            {rooms.map((r) => (
              <DropdownMenu.Item
                key={r.id}
                onSelect={() => chooseRoom(r.id)}
                className="flex cursor-default items-center gap-2 rounded-xl px-3 py-2.5 text-sm outline-none select-none data-highlighted:bg-accent"
              >
                <span className="flex-1 truncate">{r.name}</span>
                {r.id === room.id && <Check className="size-4 text-primary" />}
              </DropdownMenu.Item>
            ))}
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    ) : (
      room.name
    )

  return (
    <header className="flex items-end justify-between gap-4 pt-10 pb-6">
      <div className="min-w-0">
        <h1 className="text-display">{title}</h1>
        <p className="mt-2 flex items-center gap-2 text-sm text-muted-foreground">
          <span
            className={cn(
              'size-1.5 rounded-full',
              status === 'live' ? 'bg-success' : 'animate-pulse bg-muted-foreground',
            )}
          />
          {status === 'live'
            ? `${members.length} here now`
            : status === 'connecting'
              ? 'Connecting…'
              : 'Reconnecting…'}
        </p>
      </div>
      <div className="flex -space-x-2" aria-label="Here now">
        <AnimatePresence initial={false}>
          {members.slice(0, 5).map((m) => (
            <motion.div
              key={m.id}
              layout
              initial={{ scale: 0, opacity: 0 }}
              animate={{ scale: 1, opacity: 1 }}
              exit={{ scale: 0, opacity: 0 }}
              transition={spring}
            >
              <UserAvatar user={m} className="size-9 text-xs ring-2 ring-background" />
            </motion.div>
          ))}
        </AnimatePresence>
        {members.length > 5 && (
          <span className="grid size-9 place-items-center rounded-full bg-muted text-xs ring-2 ring-background">
            +{members.length - 5}
          </span>
        )}
      </div>
    </header>
  )
}

function NowPlayingCard({ room, waiting }: { room: RoomInfo; waiting: number }) {
  const roomId = room.id
  const { nowPlaying: np, commands } = usePlayer()
  const playback = useQuery({ ...playbackQuery(roomId), enabled: false })
  const position = usePosition(np)
  const [scrub, setScrub] = useState<number>()
  const speaker = playback.data?.player

  if (!np) {
    return (
      <motion.section variants={fadeUp} className="glass flex flex-col items-center gap-3 rounded-3xl px-6 py-10 text-center">
        <div className="grid size-14 place-items-center rounded-2xl bg-primary/15 text-primary">
          {waiting > 0 ? <Speaker className="size-7" /> : <Sparkles className="size-7" />}
        </div>
        <h2 className="text-headline">{waiting > 0 ? 'Ready when the speaker is' : 'The queue is quiet'}</h2>
        <p className="text-sm text-muted-foreground">
          {waiting > 0
            ? speaker
              ? `Waiting to start on ${speaker.name}.`
              : 'Songs are waiting. Start playing on the phone connected to the speaker.'
            : 'Be the first to put something on.'}
        </p>
        <div className="mt-2 w-full max-w-xs">
          <SpeakerPanel room={room} prominent={waiting > 0 && !speaker} />
        </div>
        {waiting === 0 && (
          <Button asChild size="lg" className="mt-2">
            <Link to="/search">
              <Plus data-icon="inline-start" />
              Find a song
            </Link>
          </Button>
        )}
      </motion.section>
    )
  }

  const shown = scrub ?? position
  return (
    <motion.section
      variants={fadeUp}
      style={laneStyle(np.requester?.color)}
      className="glass relative overflow-hidden rounded-3xl p-5"
    >
      <div className="flex gap-4">
        <AnimatePresence mode="popLayout" initial={false}>
          <motion.div
            key={np.track.trackId}
            initial={{ opacity: 0, scale: 0.9 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.9 }}
            transition={spring}
          >
            <Artwork src={np.artworkUrl} className="size-28 rounded-2xl sm:size-36" />
          </motion.div>
        </AnimatePresence>
        <div className="flex min-w-0 flex-1 flex-col justify-between">
          <div className="min-w-0">
            <p className="text-caption font-medium tracking-wide text-muted-foreground uppercase">
              {playback.data?.state === 'loading' ? 'Starting…' : np.paused ? 'Paused' : 'Now playing'}
            </p>
            <h2 className="mt-1 line-clamp-2 text-title">{np.track.title}</h2>
            <p className="truncate text-muted-foreground">{np.track.artists.join(', ')}</p>
          </div>
          {np.requester && (
            <Badge variant="lane" style={laneStyle(np.requester.color)} className="mt-2 gap-1.5 py-1 pl-1">
              <UserAvatar user={np.requester} className="size-5 text-[0.6rem]" />
              {np.requester.displayName}
            </Badge>
          )}
        </div>
      </div>

      <div className="mt-5">
        <Slider
          aria-label="Seek"
          max={np.track.durationMs}
          step={1000}
          value={[shown]}
          disabled={!commands.seek}
          onValueChange={([v]) => setScrub(v)}
          onValueCommit={([v]) => {
            commands.seek?.(v)
            setScrub(undefined)
          }}
        />
        <div className="flex justify-between text-caption text-muted-foreground tabular-nums">
          <span>{formatDuration(shown)}</span>
          <span>-{formatDuration(np.track.durationMs - shown)}</span>
        </div>
      </div>

      <div className="mt-3">
        <TransportControls paused={np.paused} commands={commands} />
      </div>
      <div className="mt-4">
        <SpeakerPanel room={room} />
      </div>
    </motion.section>
  )
}

/** Everyone else with songs waiting, and how many. */
function Lanes({ items, me, userById }: { items: QueueItem[]; me: string; userById: (id: string) => User | undefined }) {
  const counts = new Map<string, number>()
  for (const i of items) if (i.state === 'queued' && i.addedBy !== me) counts.set(i.addedBy, (counts.get(i.addedBy) ?? 0) + 1)
  if (counts.size === 0) return null
  return (
    <motion.section variants={fadeUp}>
      <SectionTitle title="Lanes" />
      <ul className="flex flex-wrap gap-2">
        {[...counts].map(([id, n]) => {
          const u = userById(id)
          return (
            <li
              key={id}
              style={laneStyle(u?.color)}
              className="glass flex items-center gap-2 rounded-full py-1.5 pr-3.5 pl-1.5 text-sm"
            >
              {u && <UserAvatar user={u} className="size-7 text-[0.65rem]" />}
              <span className="font-medium">{u?.displayName ?? 'Someone'}</span>
              <span className="text-muted-foreground tabular-nums">
                {n} song{n === 1 ? '' : 's'}
              </span>
            </li>
          )
        })}
      </ul>
    </motion.section>
  )
}

function SectionTitle({ title, count, hint }: { title: string; count?: number; hint?: string }) {
  return (
    <div className="mb-2 flex items-baseline justify-between gap-3 px-1">
      <h2 className="text-headline">
        {title}
        {count !== undefined && count > 0 && <span className="ml-2 text-muted-foreground tabular-nums">{count}</span>}
      </h2>
      {hint && <span className="text-caption text-muted-foreground">{hint}</span>}
    </div>
  )
}

function EmptyQueue() {
  return (
    <div className="glass flex items-center justify-between gap-3 rounded-3xl p-4">
      <p className="text-sm text-muted-foreground">Nothing waiting. Everyone&apos;s lane is empty.</p>
      <Button asChild size="sm" variant="secondary">
        <Link to="/search">Add a song</Link>
      </Button>
    </div>
  )
}
