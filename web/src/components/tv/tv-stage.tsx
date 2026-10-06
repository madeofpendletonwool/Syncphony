import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Sparkles } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useMemo } from 'react'
import { Artwork } from '@/components/artwork'
import { LyricsView } from '@/components/lyrics/lyrics-view'
import { AutopilotMark } from '@/components/room/autopilot-badge'
import { AlbumBackdrop } from '@/components/shell/album-backdrop'
import { UserAvatar } from '@/components/user-avatar'
import { useAlbumPalette } from '@/hooks/use-album-palette'
import { usePosition } from '@/hooks/use-position'
import { autopilotReason } from '@/lib/autopilot'
import { laneStyle } from '@/lib/lane'
import { linerCards, linerNotesQuery } from '@/lib/liner-notes'
import { inGap, lyricsQuery, offsetKey, useLyricsOffset } from '@/lib/lyrics'
import { easeOutExpo, spring } from '@/lib/motion'
import { formatDuration, type NowPlaying, type User } from '@/lib/now-playing'
import { playbackQuery, queueArtworkUrl, toNowPlaying, type QueueItem } from '@/lib/playback'
import { queueQuery } from '@/lib/room'
import { live, useRoomSocket } from '@/lib/room-socket'
import { useStore } from '@/lib/store'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'
import { FloatingReactions } from './floating-reactions'
import { LinerCards } from './liner-cards'
import { QrCode } from './qr-code'

const UP_NEXT_SHOWN = 5

/**
 * The room on a TV: huge artwork and the song's colors, lyrics front and
 * center, who queued it, whose turn is next, and reactions floating up
 * from everyone's phones. Nothing to click; it just runs.
 */
export function TvStage({
  roomId,
  roomName,
  paired,
  onUnpaired,
}: {
  roomId: string
  roomName: string
  /** A paired display, rather than a signed-in user's screen. */
  paired: boolean
  onUnpaired: () => void
}) {
  const queryClient = useQueryClient()
  useRoomSocket(roomId, { display: true, onSessionEnded: paired ? onUnpaired : undefined })
  const playback = useQuery(playbackQuery(roomId))
  const queue = useQuery(queueQuery(roomId))
  const users = useQuery(usersQuery)
  const np = useMemo(
    () => (playback.data ? toNowPlaying(roomId, playback.data, users.data) : null),
    [roomId, playback.data, users.data],
  )
  useAlbumPalette(np)
  useWakeLock()
  const { status } = useStore(live)

  // Songs queued by someone who signed up after we loaded the user list.
  useEffect(() => {
    if (!queue.data || !users.data) return
    if (queue.data.items.some((i) => !users.data.some((u) => u.id === i.addedBy))) {
      void queryClient.invalidateQueries({ queryKey: usersQuery.queryKey })
    }
  }, [queue.data, users.data, queryClient])

  const items = queue.data?.items ?? []
  const byId = new Map(items.map((i) => [i.id, i]))
  const upNext = (queue.data?.upNext ?? []).map((id) => byId.get(id)).filter((i): i is QueueItem => !!i)
  const userById = (id: string) => users.data?.find((u) => u.id === id)
  const joinUrl = `${location.origin}/room?join=${encodeURIComponent(roomId)}`

  return (
    <div className="relative isolate h-dvh cursor-none overflow-hidden select-none">
      <AlbumBackdrop src={np?.artworkUrl} />
      <div aria-hidden className="pointer-events-none fixed inset-0 -z-10 bg-[radial-gradient(ellipse_at_center,transparent_40%,rgb(0_0_0/0.55))]" />
      <FloatingReactions users={users.data} />

      <div className="burn-in-drift flex h-full flex-col gap-[3vh] px-[4vw] pt-[4vh] pb-[3.5vh]">
        <header className="flex items-center justify-between gap-6">
          <div className="flex items-center gap-3 text-[clamp(1rem,1.5vw,1.5rem)]">
            <span className={cn('size-3 rounded-full', status === 'live' ? 'bg-success shadow-[0_0_12px_var(--success)]' : 'animate-pulse bg-muted-foreground')} />
            <span className="font-semibold">{roomName}</span>
            <span className="text-muted-foreground">· Syncphony</span>
          </div>
        </header>

        <main className="grid min-h-0 flex-1 grid-cols-[minmax(0,0.9fr)_minmax(0,1.35fr)] gap-[4vw]">
          {np ? <NowPlayingColumn np={np} /> : <QuietColumn />}
          <section className="relative flex min-h-0 flex-col justify-center">
            {np ? <StageWords np={np} /> : <JoinPrompt url={joinUrl} big />}
          </section>
        </main>

        <footer className="flex items-end justify-between gap-[3vw]">
          <UpNext items={upNext.slice(0, UP_NEXT_SHOWN)} more={Math.max(0, upNext.length - UP_NEXT_SHOWN)} roomId={roomId} userById={userById} />
          {np && <JoinPrompt url={joinUrl} />}
        </footer>
      </div>
    </div>
  )
}

function NowPlayingColumn({ np }: { np: NowPlaying }) {
  const position = usePosition(np)
  const pct = np.track.durationMs > 0 ? (position / np.track.durationMs) * 100 : 0
  return (
    <section className="flex min-h-0 flex-col justify-center gap-[2.5vh] overflow-hidden">
      <AnimatePresence mode="popLayout" initial={false}>
        <motion.div
          key={np.itemId}
          initial={{ opacity: 0, scale: 0.92, filter: 'blur(12px)' }}
          animate={{ opacity: 1, scale: 1, filter: 'blur(0px)' }}
          exit={{ opacity: 0, scale: 1.04, filter: 'blur(12px)' }}
          transition={{ duration: 0.9, ease: easeOutExpo }}
          className="flex shrink-0 flex-col gap-[2.5vh]"
        >
          <Artwork
            src={np.artworkUrl}
            alt=""
            className="w-[min(34vh,100%)] rounded-[3vh] shadow-[0_40px_120px_-30px_var(--glow)]"
          />
          <div className="min-w-0">
            <h1 className="line-clamp-2 text-[clamp(1.75rem,3.4vw,3.75rem)] leading-[1.08] font-bold tracking-tight text-balance">
              {np.track.title}
            </h1>
            <p className="mt-[0.6vh] line-clamp-1 text-[clamp(1.1rem,2vw,2.1rem)] text-muted-foreground">
              {np.track.artists.join(', ')}
            </p>
            {np.autopilot && (
              <div className="mt-[2vh] flex items-center gap-3">
                <AutopilotMark className="size-[5vh] [&>svg]:size-[2.4vh]" />
                <p className="text-[clamp(0.95rem,1.5vw,1.5rem)]">
                  <span className="font-semibold text-primary">Autopilot</span>
                  <span className="text-muted-foreground"> · {autopilotReason(np.autopilot)}</span>
                </p>
              </div>
            )}
            {np.requester && (
              <div style={laneStyle(np.requester.color)} className="mt-[2vh] flex items-center gap-3">
                <UserAvatar user={np.requester} className="size-[5vh] text-[1.8vh] ring-2 ring-(--lane)" />
                <p className="text-[clamp(0.95rem,1.5vw,1.5rem)]">
                  <span className="text-muted-foreground">Queued by </span>
                  <span className="font-semibold text-(--lane)">{np.requester.displayName}</span>
                </p>
              </div>
            )}
          </div>
        </motion.div>
      </AnimatePresence>
      <div>
        <div className="h-[0.7vh] overflow-hidden rounded-full bg-foreground/15">
          <div className="h-full rounded-full bg-(--pal-text) transition-[width] duration-300 ease-linear" style={{ width: `${pct}%` }} />
        </div>
        <div className="mt-[1vh] flex justify-between text-[clamp(0.8rem,1.1vw,1.15rem)] text-muted-foreground tabular-nums">
          <span>{np.paused ? 'Paused' : formatDuration(position)}</span>
          <span>-{formatDuration(np.track.durationMs - position)}</span>
        </div>
      </div>
    </section>
  )
}

/**
 * Lyrics, or liner notes when there are none to sing: before they're
 * known, for instrumentals, and in long breaks between verses.
 */
function StageWords({ np }: { np: NowPlaying }) {
  const roomId = np.roomId ?? ''
  const itemId = np.itemId ?? ''
  const lyrics = useQuery({ ...lyricsQuery(roomId, itemId), enabled: !!itemId })
  const notes = useQuery({ ...linerNotesQuery(roomId, itemId), enabled: !!itemId })
  const cards = useMemo(() => (notes.data ? linerCards(notes.data) : []), [notes.data])
  const [offset] = useLyricsOffset(offsetKey(np.track))
  const position = usePosition(np)

  const l = lyrics.data
  const nothingToSing = lyrics.isSuccess && (!l || l.instrumental)
  const gap = !!l?.synced && inGap(l.lines, position - offset)
  const showCards = cards.length > 0 && (nothingToSing || gap)

  return (
    <AnimatePresence mode="wait" initial={false}>
      {showCards ? (
        <motion.div
          key="cards"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.6, ease: easeOutExpo }}
          className="flex flex-col justify-center"
        >
          <LinerCards cards={cards} className="max-w-[46vw]" />
        </motion.div>
      ) : (
        <motion.div
          key={`lyrics-${np.itemId}`}
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.6, ease: easeOutExpo }}
          className="flex h-full min-h-0 flex-col"
        >
          <LyricsView np={np} variant="stage" className="h-full" />
        </motion.div>
      )}
    </AnimatePresence>
  )
}

function QuietColumn() {
  return (
    <section className="flex flex-col justify-center gap-[2.5vh]">
      <span className="grid size-[10vh] place-items-center rounded-[2.5vh] bg-primary/15 text-primary">
        <Sparkles className="size-[5vh]" />
      </span>
      <h1 className="text-[clamp(2rem,4.5vw,4.75rem)] leading-tight font-bold tracking-tight">The queue is quiet</h1>
      <p className="max-w-[30ch] text-[clamp(1.1rem,1.8vw,1.9rem)] text-muted-foreground">
        Scan the code to join the room, and put something on.
      </p>
    </section>
  )
}

/** Whose turn it is next, in the fair rotation. */
function UpNext({
  items,
  more,
  roomId,
  userById,
}: {
  items: QueueItem[]
  more: number
  roomId: string
  userById: (id: string) => User | undefined
}) {
  if (items.length === 0) return <div />
  const next = items[0].autopilot ? undefined : userById(items[0].addedBy)
  return (
    <section className="min-w-0 flex-1">
      <p className="mb-[1.2vh] text-[clamp(0.8rem,1.1vw,1.15rem)] font-semibold tracking-[0.18em] text-muted-foreground uppercase">
        Up next{next && <span style={laneStyle(next.color)} className="text-(--lane) normal-case tracking-normal"> · {next.displayName}&apos;s turn</span>}
        {items[0].autopilot && <span className="text-primary normal-case tracking-normal"> · Autopilot</span>}
      </p>
      <ol className="flex gap-[1.2vw]">
        <AnimatePresence initial={false}>
          {items.map((item, i) => {
            const u = item.autopilot ? undefined : userById(item.addedBy)
            return (
              <motion.li
                key={item.id}
                layout
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: i === 0 ? 1 : 0.8, y: 0 }}
                exit={{ opacity: 0, y: -20 }}
                transition={spring}
                style={laneStyle(u?.color)}
                className={cn(
                  'glass flex w-[15vw] min-w-0 items-center gap-[0.8vw] rounded-[2vh] p-[1vh] pr-[1vw]',
                  i === 0 && 'ring-2 ring-(--lane)/70',
                )}
              >
                <Artwork src={queueArtworkUrl(roomId, item, 160)} className="size-[6.5vh] rounded-[1.2vh]" />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[clamp(0.85rem,1.15vw,1.2rem)] font-semibold">{item.track.title}</p>
                  <p className="flex items-center gap-1.5 truncate text-[clamp(0.75rem,0.95vw,1rem)] text-muted-foreground">
                    <span className={cn('size-2 shrink-0 rounded-full bg-(--lane)', item.autopilot && 'bg-primary')} />
                    {item.autopilot ? 'Autopilot' : (u?.displayName ?? 'Someone')}
                  </p>
                </div>
              </motion.li>
            )
          })}
        </AnimatePresence>
        {more > 0 && (
          <li className="flex items-center px-[0.5vw] text-[clamp(0.85rem,1.15vw,1.2rem)] text-muted-foreground">+{more} more</li>
        )}
      </ol>
    </section>
  )
}

function JoinPrompt({ url, big }: { url: string; big?: boolean }) {
  return (
    <div className={cn('glass flex shrink-0 items-center gap-[1.2vw] rounded-[2.5vh] p-[1.4vh]', big && 'w-fit flex-col gap-[2vh] self-center p-[3vh]')}>
      <div className="rounded-[1.2vh] bg-white p-[0.8vh] text-black">
        <QrCode value={url} label="Scan to join the room" className={big ? 'size-[34vh]' : 'size-[12vh]'} />
      </div>
      <p className={cn('max-w-[12ch] text-[clamp(0.85rem,1.2vw,1.25rem)] leading-snug font-medium', big && 'max-w-none text-center text-[clamp(1.1rem,1.8vw,1.9rem)]')}>
        Scan to join
        {big ? ' and add songs' : ''}
      </p>
    </div>
  )
}

/** Keeps the screen from sleeping while it's on show. */
function useWakeLock() {
  useEffect(() => {
    let lock: WakeLockSentinel | undefined
    let stopped = false
    const request = async () => {
      if (document.visibilityState !== 'visible' || !('wakeLock' in navigator)) return
      try {
        lock = await navigator.wakeLock.request('screen')
        if (stopped) void lock.release()
      } catch {
        // Not allowed (battery saver, or no user gesture yet): the TV may dim.
      }
    }
    void request()
    document.addEventListener('visibilitychange', request)
    return () => {
      stopped = true
      document.removeEventListener('visibilitychange', request)
      void lock?.release()
    }
  }, [])
}
