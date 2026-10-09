import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Heart, Sparkles } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useMemo, useState } from 'react'
import { Artwork } from '@/components/artwork'
import { LyricsView } from '@/components/lyrics/lyrics-view'
import { AutopilotMark } from '@/components/room/autopilot-badge'
import { QueueGameStage } from '@/components/games/queue-game-stage'
import { RoundStage } from '@/components/games/round-stage'
import { CrownMoment } from '@/components/room/crown-moment'
import { NightWrapped } from '@/components/wrapped/night-wrapped'
import { AlbumBackdrop } from '@/components/shell/album-backdrop'
import { Waveform } from '@/components/shell/waveform'
import { UserAvatar } from '@/components/user-avatar'
import { Visualizer } from '@/components/visualizer/visualizer'
import { useAlbumPalette } from '@/hooks/use-album-palette'
import { useBeat } from '@/hooks/use-beat'
import { useBeatSync } from '@/hooks/use-beat-sync'
import { isShow, useShow } from '@/hooks/use-scene'
import { usePosition } from '@/hooks/use-position'
import { useWakeLock } from '@/hooks/use-wake-lock'
import { autopilotReason, autopilotSource } from '@/lib/autopilot'
import { box } from '@/lib/box'
import { setBeatSettings } from '@/lib/beat'
import { useHidden } from '@/lib/games'
import { guestPassQuery } from '@/lib/guests'
import { laneStyle } from '@/lib/lane'
import { linerCards, linerNotesQuery } from '@/lib/liner-notes'
import { inGap, lyricsQuery, offsetKey, useLyricsOffset } from '@/lib/lyrics'
import { easeOutExpo, spring } from '@/lib/motion'
import { heartsQuery } from '@/lib/nights'
import { formatDuration, type NowPlaying, type PlayerCommands, type User } from '@/lib/now-playing'
import { playbackQuery, queueArtworkUrl, toNowPlaying, type QueueItem } from '@/lib/playback'
import { queueQuery, type Room } from '@/lib/room'
import { live, useRoomSocket } from '@/lib/room-socket'
import { useStore } from '@/lib/store'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'
import { FloatingReactions } from './floating-reactions'
import { LinerCards } from './liner-cards'
import { QrCode } from './qr-code'
import { TvAudio } from './tv-audio'
import { TvKeys } from './tv-keys'

type RoomScreens = Room['screens']

const UP_NEXT_SHOWN = 5

/**
 * The room on a TV: huge artwork and the song's colors, lyrics front and
 * center, who queued it, whose turn is next, and reactions floating up
 * from everyone's phones. Nothing to click; it just runs. With audio, it
 * can play the room too: one press of OK on the remote makes it the speaker.
 * Keys work too (see TvKeys): space, → to skip, ↑ and ↓ for the visuals.
 */
export function TvStage({
  roomId,
  roomName,
  screens,
  paired,
  onUnpaired,
  audio,
  commands,
}: {
  roomId: string
  roomName: string
  /** How the room's big screens look (room settings). */
  screens: RoomScreens
  /** A paired display, rather than a signed-in user's screen. */
  paired: boolean
  onUnpaired: () => void
  /** Set when this screen may be the room's speaker. */
  audio?: { device: string; name: string; onStopped?: () => void; canPlayPause?: boolean; canSkip?: boolean; autoStart?: boolean }
  /** A signed-in screen's playback controls, as its user, for the keys. */
  commands?: PlayerCommands
}) {
  const queryClient = useQueryClient()
  useRoomSocket(roomId, { display: true, onSessionEnded: paired ? onUnpaired : undefined, device: audio?.device })
  const playback = useQuery(playbackQuery(roomId))
  // A box's daemon wants to know when the room plays (ADR 0016); elsewhere this does nothing.
  useEffect(() => {
    box.playback(roomId, playback.data)
  }, [roomId, playback.data])
  const queue = useQuery(queueQuery(roomId))
  const users = useQuery(usersQuery)
  const np = useMemo(
    () => (playback.data ? toNowPlaying(roomId, playback.data, users.data) : null),
    [roomId, playback.data, users.data],
  )
  useAlbumPalette(np)
  useBeatSync(np)
  useWakeLock()
  // Big screens move harder than phones, as hard as the room says.
  useEffect(() => {
    setBeatSettings({ level: 'subtle', intensity: screens.intensity, scene: 'auto' })
  }, [screens.intensity])
  // The visualizer takes over in its look, and in auto for songs with no words to sing.
  const words = useQuery({ ...lyricsQuery(roomId, np?.itemId ?? ''), enabled: !!np?.itemId })
  const wordless = words.isSuccess && (!words.data || words.data.instrumental)
  const visualizing = !!np && (screens.look === 'visualizer' || (screens.look === 'auto' && wordless))
  // ↑ and ↓ pick a show on this screen; the room's setting until then.
  const [showPick, setShowPick] = useState<string>()
  const showSetting = showPick ?? (isShow(screens.scene) ? screens.scene : 'auto')
  const show = useShow(showSetting)
  const { status } = useStore(live)
  const pointer = usePointerShown()

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
  // With a guest pass, anyone can scan in; members get to the room either way.
  const pass = useQuery(guestPassQuery(roomId))
  const join = pass.data
    ? { url: pass.data.url, guests: true }
    : { url: `${location.origin}/room?join=${encodeURIComponent(roomId)}`, guests: false }

  return (
    <div className={cn('relative isolate h-dvh overflow-hidden select-none', !pointer && 'cursor-none')}>
      <AlbumBackdrop src={np?.artworkUrl} scene={!visualizing} />
      <div aria-hidden className="pointer-events-none fixed inset-0 -z-10 bg-[radial-gradient(ellipse_at_center,transparent_40%,rgb(0_0_0/0.55))]" />
      <AnimatePresence>
        {visualizing && (
          <motion.div
            key="visualizer"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 1.6 }}
            className="absolute inset-0"
          >
            <Visualizer show={show} artworkUrl={np?.artworkUrl} />
          </motion.div>
        )}
      </AnimatePresence>
      <FloatingReactions users={users.data} />
      <TvKeys
        roomId={roomId}
        playback={playback.data}
        commands={commands}
        display={paired && !!audio}
        visualizing={visualizing}
        show={show}
        showSetting={showSetting}
        onShowSetting={setShowPick}
      />
      <CrownMoment roomId={roomId} variant="stage" />
      <NightWrapped roomId={roomId} roomName={roomName} variant="stage" />
      <RoundStage roomId={roomId} />
      <QueueGameStage roomId={roomId} />

      <div className="burn-in-drift flex h-full flex-col gap-[3vh] px-[4vw] pt-[4vh] pb-[3.5vh]">
        <header className="flex items-center justify-between gap-6">
          <div className="flex items-center gap-3 text-[clamp(1rem,1.5vw,1.5rem)]">
            <span className={cn('size-3 rounded-full', status === 'live' ? 'bg-success shadow-[0_0_12px_var(--success)]' : 'animate-pulse bg-muted-foreground')} />
            <span className="font-semibold">{roomName}</span>
            <span className="text-muted-foreground">· Syncphony</span>
          </div>
          {audio && <TvAudio roomId={roomId} {...audio} />}
        </header>

        {visualizing && np ? (
          <footer className="mt-auto flex items-end justify-between gap-[3vw]">
            <VisualizerCaption np={np} />
            <JoinPrompt {...join} />
          </footer>
        ) : (
          <>
            <main className="grid min-h-0 flex-1 grid-cols-[minmax(0,0.9fr)_minmax(0,1.35fr)] gap-[4vw]">
              {np ? <NowPlayingColumn np={np} /> : <QuietColumn />}
              <section className="relative flex min-h-0 flex-col justify-center">
                {np ? <StageWords np={np} /> : <JoinPrompt {...join} big />}
              </section>
            </main>

            <footer className="flex items-end justify-between gap-[3vw]">
              <UpNext items={upNext.slice(0, UP_NEXT_SHOWN)} more={Math.max(0, upNext.length - UP_NEXT_SHOWN)} roomId={roomId} userById={userById} />
              {np && <JoinPrompt {...join} />}
            </footer>
          </>
        )}
      </div>
    </div>
  )
}

/** What's playing, small in a corner while the visualizer has the screen. */
function VisualizerCaption({ np }: { np: NowPlaying }) {
  const position = usePosition(np)
  return (
    <AnimatePresence mode="popLayout" initial={false}>
      <motion.div
        key={np.itemId}
        initial={{ opacity: 0, y: 16 }}
        animate={{ opacity: 1, y: 0 }}
        exit={{ opacity: 0, y: -16 }}
        transition={{ duration: 0.8, ease: easeOutExpo }}
        className="glass flex max-w-[55vw] min-w-0 items-center gap-[1.5vw] rounded-[2.4vh] p-[1.2vh] pr-[2vw]"
      >
        <Artwork src={np.artworkUrl} alt="" className="size-[11vh] rounded-[1.6vh]" />
        <div className="min-w-0">
          <p className="line-clamp-1 text-[clamp(1.2rem,2.2vw,2.4rem)] leading-tight font-bold">{np.track.title}</p>
          <p className="line-clamp-1 text-[clamp(0.95rem,1.5vw,1.6rem)] text-muted-foreground">
            {np.track.artists.join(', ')}
            {np.requester && (
              <span style={laneStyle(np.requester.color)}>
                {' · '}
                <span className="text-(--lane)">{np.requester.displayName}</span>
              </span>
            )}
          </p>
          <Waveform itemId={np.itemId} positionMs={position} durationMs={np.track.durationMs} className="mt-[1vh] h-[3vh] w-[18vw]" />
        </div>
      </motion.div>
    </AnimatePresence>
  )
}

function NowPlayingColumn({ np }: { np: NowPlaying }) {
  const position = usePosition(np)
  const beat = useBeat<HTMLDivElement>()
  return (
    // The artwork gives way to a long title on a short screen: it shrinks to
    // the height that's left, so the title and the times always fit.
    <section className="flex min-h-0 flex-col justify-center-safe gap-[2.5vh] overflow-hidden">
      <AnimatePresence mode="popLayout" initial={false}>
        <motion.div
          key={np.itemId}
          initial={{ opacity: 0, scale: 0.92, filter: 'blur(12px)' }}
          animate={{ opacity: 1, scale: 1, filter: 'blur(0px)' }}
          exit={{ opacity: 0, scale: 1.04, filter: 'blur(12px)' }}
          transition={{ duration: 0.9, ease: easeOutExpo }}
          className="flex min-h-0 flex-col gap-[2.5vh]"
        >
          <div className="flex min-h-[10vh] flex-[0_1_34vh]">
            {/* Lifts on the one, with a bloom of the art's color behind it, as in the expanded player. */}
            <div ref={beat} className="beat-lift relative aspect-square h-full max-w-full">
              <div aria-hidden className="beat-bloom pointer-events-none absolute -inset-[12%] -z-10 rounded-full blur-2xl" />
              <Artwork
                src={np.artworkUrl}
                alt=""
                className="size-full rounded-[3vh] shadow-[0_40px_120px_-30px_var(--glow)]"
              />
            </div>
          </div>
          <div className="min-w-0 shrink-0">
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
                  {autopilotSource(np.autopilot) && (
                    <span className="text-muted-foreground/70"> · {autopilotSource(np.autopilot)}</span>
                  )}
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
            <StageHearts np={np} />
          </div>
        </motion.div>
      </AnimatePresence>
      <div className="shrink-0">
        {/* The song's shape instead of a plain progress bar: nobody scrubs a TV. */}
        <Waveform itemId={np.itemId} positionMs={position} durationMs={np.track.durationMs} className="h-[5vh]" />
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
  const hidden = useHidden(roomId, itemId)
  const lyrics = useQuery({ ...lyricsQuery(roomId, itemId), enabled: !!itemId && !hidden.lyrics })
  const notes = useQuery({ ...linerNotesQuery(roomId, itemId), enabled: !!itemId && !hidden.notes && !hidden.song })
  // A round about the song keeps its notes off the screen until the reveal.
  const cards = useMemo(() => (notes.data && !hidden.notes && !hidden.song ? linerCards(notes.data) : []), [notes.data, hidden.notes, hidden.song])
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

/** Hearts toward song of the night, as they come in. */
function StageHearts({ np }: { np: NowPlaying }) {
  const hearts = useQuery({ ...heartsQuery(np.roomId ?? '', np.itemId ?? ''), enabled: !!np.itemId })
  const n = hearts.data?.userIds.length ?? 0
  return (
    <AnimatePresence>
      {n > 0 && (
        <motion.p
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0 }}
          className="mt-[1.5vh] flex items-center gap-2 text-[clamp(0.9rem,1.4vw,1.4rem)] text-muted-foreground"
        >
          <motion.span key={n} initial={{ scale: 1.6 }} animate={{ scale: 1 }} transition={spring}>
            <Heart className="size-[2.6vh] fill-rose-500 text-rose-500" />
          </motion.span>
          <span className="tabular-nums">
            {n} {n === 1 ? 'heart' : 'hearts'}
          </span>
        </motion.p>
      )}
    </AnimatePresence>
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
                  {item.autopilot && (
                    <p className="truncate text-[clamp(0.7rem,0.85vw,0.9rem)] text-primary" title={autopilotReason(item.autopilot)}>
                      {autopilotReason(item.autopilot)}
                    </p>
                  )}
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

function JoinPrompt({ url, guests, big }: { url: string; guests: boolean; big?: boolean }) {
  return (
    <div className={cn('glass flex shrink-0 items-center gap-[1.2vw] rounded-[2.5vh] p-[1.4vh]', big && 'w-fit flex-col gap-[2vh] self-center p-[3vh]')}>
      <div className="rounded-[1.2vh] bg-white p-[0.8vh] text-black">
        <QrCode value={url} label="Scan to join the room" className={big ? 'size-[34vh]' : 'size-[12vh]'} />
      </div>
      <p className={cn('max-w-[12ch] text-[clamp(0.85rem,1.2vw,1.25rem)] leading-snug font-medium', big && 'max-w-none text-center text-[clamp(1.1rem,1.8vw,1.9rem)]')}>
        Scan to join
        {big ? ' and add songs' : ''}
        {guests && <span className="block text-[0.85em] font-normal text-muted-foreground">No account needed</span>}
      </p>
    </div>
  )
}

// How long the mouse pointer stays up after it last moved.
const POINTER_SHOWN = 3000

/** Whether the mouse moved lately: the pointer hides otherwise, but a desktop needs it to press the buttons. */
function usePointerShown() {
  const [shown, setShown] = useState(false)
  useEffect(() => {
    let t: number | undefined
    const moved = (e: PointerEvent) => {
      if (e.pointerType !== 'mouse') return
      setShown(true)
      window.clearTimeout(t)
      t = window.setTimeout(() => setShown(false), POINTER_SHOWN)
    }
    window.addEventListener('pointermove', moved)
    return () => {
      window.removeEventListener('pointermove', moved)
      window.clearTimeout(t)
    }
  }, [])
  return shown
}
