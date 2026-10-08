import { useQuery } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { Check, ChevronDown, Crown, History, LogOut, MonitorPlay, Play, Plus, QrCode, Settings2, Sparkles, Speaker, Users } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { DropdownMenu } from 'radix-ui'
import { useEffect, useState, type ReactNode } from 'react'
import { errorMessage } from '@/api/errors'
import { Artwork } from '@/components/artwork'
import { PageHeader } from '@/components/page-header'
import { AutopilotBadge } from '@/components/room/autopilot-badge'
import { BigScreenDialog } from '@/components/room/big-screen-dialog'
import { GuestsDialog } from '@/components/room/guests-dialog'
import { HeartButton } from '@/components/room/heart-button'
import { MembersDialog } from '@/components/room/members-dialog'
import { MyLane } from '@/components/room/my-lane'
import { ReactionBar } from '@/components/room/reaction-bar'
import { RecentlyPlayed } from '@/components/room/recently-played'
import { RoomSettings } from '@/components/room/room-settings'
import { SongDetails } from '@/components/room/song-details'
import { SpeakerPanel } from '@/components/room/speaker-panel'
import { UpNext } from '@/components/room/up-next'
import { TransportControls } from '@/components/shell/player-controls'
import { SourceTag } from '@/components/service-tag'
import { RoomLobby } from '@/components/start-room'
import { AlbumLink, ArtistLinks } from '@/components/track-credits'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Slider } from '@/components/ui/slider'
import { UserAvatar } from '@/components/user-avatar'
import { usePosition } from '@/hooks/use-position'
import { useQueueRemoval } from '@/hooks/use-queue-removal'
import { visibility } from '@/lib/access'
import { useMe } from '@/lib/auth'
import { isMine } from '@/lib/autopilot'
import { laneStyle } from '@/lib/lane'
import { fadeUp, spring, stagger } from '@/lib/motion'
import { endNight } from '@/lib/nights'
import { formatDuration, usePlayer, type User } from '@/lib/now-playing'
import { playbackQuery, songsBeforeYours, type QueueItem } from '@/lib/playback'
import { chooseRoom, leaveRoom, queueQuery, useCurrentRoom, type Room as RoomInfo } from '@/lib/room'
import { live } from '@/lib/room-socket'
import { useStore } from '@/lib/store'
import { relativeTime } from '@/lib/time'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

export const Route = createFileRoute('/_app/_authed/room')({
  // ?join=<roomId>: the big screen's QR code drops you into its room.
  validateSearch: (search: Record<string, unknown>): { join?: string } =>
    typeof search.join === 'string' && search.join ? { join: search.join } : {},
  component: Room,
})

// How much of the fair order to show before summarizing the rest.
const UP_NEXT_SHOWN = 12
// How many of the songs just played to show; History has the rest.
const RECENT_SHOWN = 5

function Room() {
  const me = useMe()
  const { room, rooms } = useCurrentRoom()
  const { join } = Route.useSearch()
  const navigate = Route.useNavigate()
  const { nowPlaying: np, commands } = usePlayer()
  useEffect(() => {
    if (!join || !rooms.data) return
    if (rooms.data.some((r) => r.id === join)) chooseRoom(join)
    void navigate({ search: {}, replace: true })
  }, [join, rooms.data, navigate])
  // A guest belongs to one room; they're always in it.
  const guestRoom = me.guest?.roomId
  useEffect(() => {
    if (guestRoom && !room && rooms.data?.some((r) => r.id === guestRoom)) chooseRoom(guestRoom)
  }, [guestRoom, room, rooms.data])
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
        <PageHeader title="Rooms" subtitle="Join a room, or start your own. Everyone takes turns." />
        <RoomLobby />
      </>
    )
  }

  const items = queue.data?.items ?? []
  const byId = new Map(items.map((i) => [i.id, i]))
  const upNext = (queue.data?.upNext ?? []).map((id) => byId.get(id)).filter((i): i is QueueItem => !!i)
  const mine = items.filter((i) => isMine(i, me.id) && i.state === 'queued').sort((a, b) => a.lanePosition - b.lanePosition)
  const before = songsBeforeYours(queue.data?.upNext ?? [], items, me.id)
  const owner = room.ownerId === me.id

  return (
    <>
      <RoomHeader room={room} rooms={rooms.data ?? []} />

      <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-6">
        <NowPlayingCard room={room} waiting={upNext.length} />
        {np && <ReactionBar roomId={room.id} />}
        {np && <SongDetails np={np} commands={commands} />}

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

        {me.guest && <GuestNote room={room} added={items.filter((i) => i.addedBy === me.id && !i.autopilot).length} />}

        <motion.section variants={fadeUp}>
          <SectionTitle title="Up next" count={upNext.length} hint={mine.length > 1 ? 'Drag to reorder' : undefined} />
          {upNext.length === 0 ? (
            <EmptyQueue />
          ) : (
            <UpNext roomId={room.id} items={upNext} limit={UP_NEXT_SHOWN} me={me.id} owner={owner} userById={userById} />
          )}
        </motion.section>

        <motion.section variants={fadeUp}>
          <SectionTitle
            title="Your lane"
            count={mine.length}
            hint={mine.length > 1 ? 'Drag to reorder · swipe to remove' : undefined}
            action={mine.length > 1 && <ClearLane roomId={room.id} />}
          />
          <MyLane roomId={room.id} items={mine} me={me} />
        </motion.section>

        <Lanes items={items} me={me.id} userById={userById} />

        <motion.div variants={fadeUp}>
          <RecentlyPlayed roomId={room.id} itemId={np?.itemId} limit={RECENT_SHOWN} />
        </motion.div>
      </motion.div>
    </>
  )
}

function RoomHeader({ room, rooms }: { room: RoomInfo; rooms: RoomInfo[] }) {
  const me = useMe()
  const { members, status } = useStore(live)
  const [settings, setSettings] = useState(false)
  const [bigScreen, setBigScreen] = useState(false)
  const [guests, setGuests] = useState(false)
  const [membersOpen, setMembersOpen] = useState(false)
  const guest = !!me.guest
  const v = visibility(room.visibility)
  const host = me.role === 'admin' || room.ownerId === me.id
  const others = guest ? [] : rooms.filter((r) => r.id !== room.id)
  const title = (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger className="-ml-1 flex max-w-full items-center gap-1.5 rounded-xl px-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
        <span className="truncate" title={room.name}>{room.name}</span>
        <ChevronDown className="size-6 shrink-0 text-muted-foreground" />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          sideOffset={8}
          className="glass-strong z-50 min-w-60 rounded-2xl p-1.5 shadow-float data-[state=open]:animate-in data-[state=open]:fade-in data-[state=open]:zoom-in-95"
        >
          <DropdownMenu.Item className={menuItem} disabled>
            <span className="flex-1 truncate font-medium" title={room.name}>{room.name}</span>
            <Check className="size-4 text-primary" />
          </DropdownMenu.Item>
          {others.length > 0 && (
            <DropdownMenu.Label className="px-3 pt-2 pb-1 text-caption text-muted-foreground">Switch to</DropdownMenu.Label>
          )}
          {others.map((r) => (
            <DropdownMenu.Item key={r.id} onSelect={() => chooseRoom(r.id)} className={menuItem}>
              <span className="flex-1 truncate" title={r.name}>{r.name}</span>
            </DropdownMenu.Item>
          ))}
          <DropdownMenu.Separator className="mx-2 my-1.5 h-px bg-border" />
          <DropdownMenu.Item asChild className={menuItem}>
            <Link to="/history">
              <History className="size-4 text-muted-foreground" />
              <span className="flex-1">History and recaps</span>
            </Link>
          </DropdownMenu.Item>
          {!guest && (
            <DropdownMenu.Item onSelect={() => setBigScreen(true)} className={menuItem}>
              <MonitorPlay className="size-4 text-muted-foreground" />
              <span className="flex-1">Big screen</span>
            </DropdownMenu.Item>
          )}
          {!guest && room.visibility !== 'open' && (
            <DropdownMenu.Item onSelect={() => setMembersOpen(true)} className={menuItem}>
              <Users className="size-4 text-muted-foreground" />
              <span className="flex-1">Members</span>
            </DropdownMenu.Item>
          )}
          {!guest && (room.guests.allowed || room.ownerId === me.id) && (
            <DropdownMenu.Item onSelect={() => setGuests(true)} className={menuItem}>
              <QrCode className="size-4 text-muted-foreground" />
              <span className="flex-1">Invite guests</span>
            </DropdownMenu.Item>
          )}
          {host && (
            <DropdownMenu.Item
              onSelect={() => endNight(room.id).catch((e: unknown) => toast({ message: errorMessage(e), tone: 'error' }))}
              className={menuItem}
            >
              <Crown className="size-4 text-muted-foreground" />
              <span className="flex-1">End the night</span>
            </DropdownMenu.Item>
          )}
          {host && !guest && (
            <DropdownMenu.Item onSelect={() => setSettings(true)} className={menuItem}>
              <Settings2 className="size-4 text-muted-foreground" />
              <span className="flex-1">Room settings</span>
            </DropdownMenu.Item>
          )}
          {!guest && (
            <DropdownMenu.Item onSelect={leaveRoom} className={menuItem}>
              <LogOut className="size-4 text-muted-foreground" />
              <span className="flex-1">Leave room</span>
            </DropdownMenu.Item>
          )}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )

  return (
    <header className="flex flex-wrap items-end justify-between gap-x-4 gap-y-3 pt-10 pb-6">
      <RoomSettings
        room={room}
        open={settings}
        onOpenChange={setSettings}
        onMembers={() => {
          setSettings(false)
          setMembersOpen(true)
        }}
      />
      <MembersDialog room={room} open={membersOpen} onOpenChange={setMembersOpen} />
      <BigScreenDialog room={room} open={bigScreen} onOpenChange={setBigScreen} />
      <GuestsDialog room={room} open={guests} onOpenChange={setGuests} />
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
          {room.visibility !== 'open' && !guest && (
            <button
              type="button"
              onClick={() => setMembersOpen(true)}
              title={v.hint}
              className="flex items-center gap-1 rounded-full outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              · <v.icon className="size-3.5" />
              {v.label}
            </button>
          )}
        </p>
      </div>
      <div className="flex shrink-0 -space-x-2" aria-label="Here now">
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

const menuItem =
  'flex cursor-default items-center gap-2 rounded-xl px-3 py-2.5 text-sm outline-none select-none data-disabled:opacity-100 data-highlighted:bg-accent'

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
        <h2 className="text-headline">
          {waiting > 0 ? (speaker ? 'Stopped' : 'Ready when the speaker is') : 'The queue is quiet'}
        </h2>
        <p className="text-sm text-muted-foreground">
          {waiting > 0
            ? speaker
              ? `${speaker.name} is the speaker. Press play to start the next song.`
              : 'Songs are waiting. Start playing on the device connected to the speaker.'
            : 'Be the first to put something on.'}
        </p>
        {waiting > 0 && speaker && commands.toggle && (
          <Button size="lg" onClick={commands.toggle} className="mt-2 w-full max-w-xs">
            <Play data-icon="inline-start" className="fill-current" />
            Play
          </Button>
        )}
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
            <ArtistLinks track={np.track} className="line-clamp-1 text-muted-foreground" />
            <AlbumLink track={np.track} className="line-clamp-1 text-sm text-muted-foreground/80" />
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {np.requester && (
              <Badge variant="lane" style={laneStyle(np.requester.color)} className="gap-1.5 py-1 pl-1">
                <UserAvatar user={np.requester} className="size-5 text-[0.6rem]" />
                {np.requester.displayName}
              </Badge>
            )}
            {np.autopilot && <AutopilotBadge pick={np.autopilot} />}
            <SourceTag provider={np.track.provider} via={np.via} className="py-1" />
            <HeartButton room={room} np={np} className="ml-auto" />
          </div>
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
  for (const i of items) {
    if (i.state === 'queued' && !i.autopilot && i.addedBy !== me) counts.set(i.addedBy, (counts.get(i.addedBy) ?? 0) + 1)
  }
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
              {u?.guest && <span className="text-caption text-muted-foreground">guest</span>}
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

/** For guests: how many songs they have left, and when their pass ends. */
function GuestNote({ room, added }: { room: RoomInfo; added: number }) {
  const me = useMe()
  const limit = room.guests.maxSongs
  const left = Math.max(limit - added, 0)
  return (
    <motion.p variants={fadeUp} className="glass rounded-2xl px-4 py-3 text-sm text-muted-foreground">
      <span className="font-medium text-foreground">You&apos;re a guest.</span>{' '}
      {limit > 0 ? `${left} of ${limit} songs left` : 'Add as many songs as you like'}
      {me.guest && ` · your pass ends ${relativeTime(me.guest.expiresAt)}`}.
    </motion.p>
  )
}

function SectionTitle({ title, count, hint, action }: { title: string; count?: number; hint?: string; action?: ReactNode }) {
  return (
    <div className="mb-2 flex items-baseline justify-between gap-3 px-1">
      <h2 className="text-headline">
        {title}
        {count !== undefined && count > 0 && <span className="ml-2 text-muted-foreground tabular-nums">{count}</span>}
      </h2>
      {(hint || action) && (
        <span className="flex items-baseline gap-3">
          {hint && <span className="text-caption text-muted-foreground">{hint}</span>}
          {action}
        </span>
      )}
    </div>
  )
}

/** Empties your lane, with a moment to Undo. */
function ClearLane({ roomId }: { roomId: string }) {
  const { clear } = useQueueRemoval(roomId)
  return (
    <button
      type="button"
      onClick={() => clear.mutate()}
      disabled={clear.isPending}
      className="rounded-full text-sm font-medium text-muted-foreground outline-none hover:text-destructive focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
    >
      Clear
    </button>
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
