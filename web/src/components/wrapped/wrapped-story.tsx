import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Crown, Flame, Heart, ListMusic, LoaderCircle, Share2, SkipForward, Sparkles, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Artwork } from '@/components/artwork'
import { SaveNightButton } from '@/components/playlists/save-night'
import { QrCode } from '@/components/tv/qr-code'
import { Button } from '@/components/ui/button'
import { UserAvatar } from '@/components/user-avatar'
import { formatListening, sessionName } from '@/lib/history'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo } from '@/lib/motion'
import type { User } from '@/lib/now-playing'
import { queueArtworkUrl } from '@/lib/playback'
import { recapQuery, type Recap } from '@/lib/playlists'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'
import { drawWrappedCard, shareCard } from '@/lib/wrapped-card'
import { overlapLine, PAGE_MS, shareBars, wrappedPages, type WrappedPage } from '@/lib/wrapped'

type Props = {
  roomId: string
  roomName: string
  from: string
  to: string
  /** Phones tap through and share; the big screen plays it through once. */
  variant?: 'phone' | 'stage'
  onClose: () => void
}

// Each page's sky.
const skies: Record<WrappedPage, string> = {
  intro: 'from-[#1b0f3d] via-[#3b1466] to-[#0d1b4a]',
  crown: 'from-[#2a1606] via-[#5a2d0c] to-[#3a0c2a]',
  people: 'from-[#06233a] via-[#0b4a5e] to-[#1a1446]',
  artists: 'from-[#2b0a3d] via-[#5b1458] to-[#12204a]',
  mix: 'from-[#0a2a22] via-[#14504a] to-[#2a1446]',
  overlap: 'from-[#1a1036] via-[#3e1a5e] to-[#5b1a3a]',
  skipped: 'from-[#2e0b12] via-[#4a1424] to-[#1b1036]',
  outro: 'from-[#120a2e] via-[#2a0f45] to-[#5b1a3a]',
}

/**
 * A night's Syncphony Wrapped (MAD-722): its recap as a story, a page at
 * a time. On phones you tap through it and share it as an image; on the
 * big screen it plays at the end of the night.
 */
export function WrappedStory({ roomId, roomName, from, to, variant = 'phone', onClose }: Props) {
  const recap = useQuery(recapQuery(roomId, from, to))
  const stage = variant === 'stage'
  useEffect(() => {
    if (recap.isError && stage) onClose()
  }, [recap.isError, stage, onClose])

  // On the body: it covers everything, whatever it's opened from.
  return createPortal(
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.5, ease: easeOutExpo }}
      role="dialog"
      aria-modal
      aria-label="Syncphony Wrapped"
      className="fixed inset-0 z-[65] grid place-items-center bg-black/80 backdrop-blur-xl"
    >
      {recap.data ? (
        <Story recap={recap.data} roomId={roomId} roomName={roomName} from={from} to={to} stage={stage} onClose={onClose} />
      ) : recap.isError ? (
        <div className="flex flex-col items-center gap-4 text-white">
          <p>Couldn&apos;t load the recap.</p>
          <Button variant="glass" onClick={onClose}>
            Close
          </Button>
        </div>
      ) : (
        <LoaderCircle className="size-8 animate-spin text-white/70" />
      )}
    </motion.div>,
    document.body,
  )
}

function Story({ recap, roomId, roomName, from, to, stage, onClose }: { recap: Recap; roomId: string; roomName: string; from: string; to: string; stage: boolean; onClose: () => void }) {
  const users = useQuery(usersQuery)
  const pages = wrappedPages(recap)
  const [at, setAt] = useState(0)
  const [held, setHeld] = useState(false)
  const page = pages[at]
  const last = at === pages.length - 1
  const user = useCallback((id: string) => users.data?.find((u) => u.id === id), [users.data])

  const next = useCallback(() => {
    if (last) {
      if (stage) onClose()
      return
    }
    setAt((a) => a + 1)
  }, [last, stage, onClose])
  const back = () => setAt((a) => Math.max(0, a - 1))

  // Moves on by itself; holding a finger down pauses it. Phones stop on the
  // last page, for sharing; the big screen closes after it.
  useEffect(() => {
    if (held || (last && !stage)) return
    const t = setTimeout(next, PAGE_MS)
    return () => clearTimeout(t)
  }, [at, held, last, stage, next])

  useEffect(() => {
    if (stage) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
      else if (e.key === 'ArrowRight' || e.key === ' ') next()
      else if (e.key === 'ArrowLeft') back()
      else return
      e.preventDefault()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [stage, next, onClose])

  const tapStart = useRef(0)

  return (
    <div
      className={cn(
        'relative flex flex-col overflow-hidden bg-gradient-to-br text-white shadow-float transition-colors duration-700',
        skies[page],
        stage ? 'h-dvh w-dvw' : 'h-dvh w-full sm:h-[min(92dvh,52rem)] sm:w-[min(100%,29rem)] sm:rounded-[2rem]',
      )}
    >
      <Bars count={pages.length} at={last && !stage ? at + 1 : at} paused={held} stage={stage} />
      {!stage && (
        <Button size="icon-sm" variant="ghost" aria-label="Close" onClick={onClose} className="absolute top-7 right-3 z-20 text-white hover:bg-white/10 hover:text-white">
          <X />
        </Button>
      )}
      {/* Tap the left third to go back, anywhere else to go on. */}
      {!stage && (
        <div
          className="absolute inset-0 z-10"
          onPointerDown={() => {
            tapStart.current = Date.now()
            setHeld(true)
          }}
          onPointerUp={(e) => {
            setHeld(false)
            if (Date.now() - tapStart.current > 400) return
            const r = e.currentTarget.getBoundingClientRect()
            if (e.clientX - r.left < r.width / 3) back()
            else next()
          }}
          onPointerCancel={() => setHeld(false)}
          onPointerLeave={() => setHeld(false)}
        />
      )}
      <AnimatePresence mode="wait">
        <motion.div
          key={page}
          initial={{ opacity: 0, y: 24, scale: 0.98 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={{ opacity: 0, y: -16 }}
          transition={{ duration: 0.6, ease: easeOutExpo }}
          className={cn('pointer-events-none relative z-20 flex flex-1 flex-col justify-center', stage ? 'gap-[4vh] px-[8vw]' : 'gap-6 px-7 pt-14 pb-10')}
        >
          <PageBody page={page} recap={recap} roomId={roomId} roomName={roomName} from={from} to={to} stage={stage} user={user} />
        </motion.div>
      </AnimatePresence>
    </div>
  )
}

function Bars({ count, at, paused, stage }: { count: number; at: number; paused: boolean; stage: boolean }) {
  return (
    <div className={cn('absolute inset-x-0 top-0 z-20 flex gap-1', stage ? 'px-[4vw] pt-[3vh]' : 'px-3 pt-3')}>
      {Array.from({ length: count }, (_, i) => (
        <span key={i} className={cn('relative flex-1 overflow-hidden rounded-full bg-white/25', stage ? 'h-1.5' : 'h-1')}>
          {i < at && <span className="absolute inset-0 bg-white" />}
          {i === at && (
            <motion.span
              key={`${at}`}
              className="absolute inset-y-0 left-0 bg-white"
              initial={{ width: '0%' }}
              animate={paused ? undefined : { width: '100%' }}
              transition={{ duration: PAGE_MS / 1000, ease: 'linear' }}
            />
          )}
        </span>
      ))}
    </div>
  )
}

type BodyProps = {
  page: WrappedPage
  recap: Recap
  roomId: string
  roomName: string
  from: string
  to: string
  stage: boolean
  user: (id: string) => User | undefined
}

function Kicker({ children, stage, className }: { children: ReactNode; stage: boolean; className?: string }) {
  return <p className={cn('font-semibold tracking-[0.2em] uppercase', stage ? 'text-[clamp(1rem,1.8vw,2rem)]' : 'text-caption', className ?? 'text-white/70')}>{children}</p>
}

function Big({ children, stage }: { children: ReactNode; stage: boolean }) {
  return <h2 className={cn('font-bold tracking-tight text-balance', stage ? 'text-[clamp(2.5rem,6vw,6.5rem)] leading-[1.05]' : 'text-[2.4rem] leading-[1.05]')}>{children}</h2>
}

function Line({ children, stage }: { children: ReactNode; stage: boolean }) {
  return <p className={cn('text-white/80', stage ? 'text-[clamp(1.2rem,2.2vw,2.5rem)]' : 'text-lg')}>{children}</p>
}

function Person({ user, stage, children }: { user?: User; stage: boolean; children: ReactNode }) {
  return (
    <div style={laneStyle(user?.color)} className={cn('flex items-center', stage ? 'gap-[1.5vw]' : 'gap-4')}>
      {user && <UserAvatar user={user} className={cn('ring-2 ring-(--lane)', stage ? 'size-[9vh] text-[3vh]' : 'size-14 text-lg')} />}
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function PageBody({ page, recap: r, roomId, roomName, from, to, stage, user }: BodyProps) {
  const name = (id: string) => user(id)?.displayName ?? 'Someone'
  const s = r.stats
  const title = sessionName({ startedAt: from })

  switch (page) {
    case 'intro':
      return (
        <>
          <Kicker stage={stage} className="text-amber-200">
            <Sparkles className="mr-2 inline size-[1em]" />
            {roomName} · Wrapped
          </Kicker>
          <Big stage={stage}>{title}</Big>
          <Line stage={stage}>
            {s.plays} {s.plays === 1 ? 'song' : 'songs'}
            {s.listeningMs >= 60_000 && `, ${formatListening(s.listeningMs)} of music`}
            {s.people.length > 1 ? `, ${s.people.length} people bringing it` : ''}.
          </Line>
          <div className="flex -space-x-2">
            {s.people.slice(0, 8).map((p, i) => {
              const u = user(p.userId)
              return u ? (
                <motion.span key={p.userId} initial={{ opacity: 0, scale: 0.6 }} animate={{ opacity: 1, scale: 1 }} transition={{ delay: 0.3 + i * 0.08 }}>
                  <UserAvatar user={u} className={cn('ring-2 ring-black/40', stage ? 'size-[8vh] text-[2.6vh]' : 'size-12')} />
                </motion.span>
              ) : null
            })}
          </div>
        </>
      )
    case 'crown': {
      const song = r.night?.songOfTheNight
      return (
        <>
          {song && (
            <>
              <Kicker stage={stage} className="text-amber-300">
                <Crown className="mr-2 inline size-[1em]" />
                Song of the night
              </Kicker>
              <Artwork
                src={queueArtworkUrl(roomId, song.item, stage ? 640 : 400)}
                className={cn('shadow-[0_30px_100px_-20px_rgb(251_191_36/0.55)] ring-2 ring-amber-300/60', stage ? 'size-[34vh] rounded-[3vh]' : 'size-52 rounded-3xl')}
              />
              <div>
                <Big stage={stage}>{song.item.track.title}</Big>
                <Line stage={stage}>
                  {song.item.track.artists.join(', ')} · <Heart className="inline size-[0.9em] fill-rose-400 text-rose-400" /> {song.hearts}
                </Line>
              </div>
            </>
          )}
          {r.mostHearted && r.mostHearted.count > 0 && (
            <Person user={user(r.mostHearted.userId)} stage={stage}>
              <Line stage={stage}>
                <span className="font-semibold text-white">{name(r.mostHearted.userId)}</span>&apos;s songs got the most love:{' '}
                {r.mostHearted.count} {r.mostHearted.count === 1 ? 'heart' : 'hearts'}.
              </Line>
            </Person>
          )}
        </>
      )
    }
    case 'people':
      return (
        <>
          <Kicker stage={stage}>Who brought it</Kicker>
          {r.topAdder && (
            <Person user={user(r.topAdder.userId)} stage={stage}>
              <Big stage={stage}>{name(r.topAdder.userId)}</Big>
              <Line stage={stage}>
                kept the queue fed: {r.topAdder.count} {r.topAdder.count === 1 ? 'song' : 'songs'}.
              </Line>
            </Person>
          )}
          {r.streak && (
            <Person user={user(r.streak.userId)} stage={stage}>
              <Line stage={stage}>
                <Flame className="mr-1 inline size-[1em] text-orange-300" />
                <span className="font-semibold text-white">{name(r.streak.userId)}</span> went {r.streak.count} songs in a row without a skip.
              </Line>
            </Person>
          )}
        </>
      )
    case 'artists':
      return (
        <>
          <Kicker stage={stage}>On repeat</Kicker>
          <ol className={cn('flex flex-col', stage ? 'gap-[2vh]' : 'gap-3')}>
            {s.topArtists.slice(0, 5).map((a, i) => (
              <motion.li
                key={a.name}
                initial={{ opacity: 0, x: -16 }}
                animate={{ opacity: 1, x: 0 }}
                transition={{ delay: 0.15 + i * 0.1, ease: easeOutExpo, duration: 0.6 }}
                className={cn('flex items-baseline gap-4 font-bold', stage ? 'text-[clamp(1.6rem,3.6vw,4rem)]' : 'text-2xl')}
              >
                <span className="w-[1.2em] text-white/50 tabular-nums">{i + 1}</span>
                <span className="min-w-0 truncate">{a.name}</span>
              </motion.li>
            ))}
          </ol>
          {s.topTracks[0] && (
            <Line stage={stage}>
              Most played: <span className="font-semibold text-white">{s.topTracks[0].item.track.title}</span>
              {s.topTracks[0].plays > 1 ? `, ${s.topTracks[0].plays} times` : ''}.
            </Line>
          )}
        </>
      )
    case 'mix':
      return (
        <>
          <Kicker stage={stage}>The mix</Kicker>
          {r.genres.length > 0 && <Bars2 shares={r.genres} stage={stage} color="bg-pink-400/80" />}
          {r.decades.length > 0 && (
            <>
              <Line stage={stage}>Across the years</Line>
              <Bars2 shares={r.decades} stage={stage} color="bg-teal-300/80" />
            </>
          )}
        </>
      )
    case 'overlap':
      return (
        <>
          <Kicker stage={stage}>Taste twins</Kicker>
          {r.overlaps.map((o, i) => {
            const [a, b] = o.userIds
            return (
              <motion.div key={`${a}-${b}`} initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.2 + i * 0.15 }} className="flex items-center gap-4">
                <span className="flex shrink-0 -space-x-3">
                  {[a, b].map((id) => {
                    const u = user(id)
                    return u ? <UserAvatar key={id} user={u} className={cn('ring-2 ring-black/40', stage ? 'size-[8vh] text-[2.6vh]' : 'size-12')} /> : null
                  })}
                </span>
                <Line stage={stage}>{overlapLine([name(a), name(b)], o.artists)}.</Line>
              </motion.div>
            )
          })}
        </>
      )
    case 'skipped': {
      const m = r.mostSkipped!
      return (
        <>
          <Kicker stage={stage}>
            <SkipForward className="mr-2 inline size-[1em]" />
            Not tonight
          </Kicker>
          <Artwork src={queueArtworkUrl(roomId, m.item, 400)} className={cn('opacity-80 grayscale', stage ? 'size-[26vh] rounded-[3vh]' : 'size-40 rounded-3xl')} />
          <Big stage={stage}>{m.item.track.title}</Big>
          <Line stage={stage}>{m.skips > 1 ? `skipped ${m.skips} times. The room had spoken.` : 'got skipped. Maybe next time.'}</Line>
        </>
      )
    }
    case 'outro':
      return <Outro recap={r} roomId={roomId} roomName={roomName} from={from} to={to} stage={stage} title={title} name={name} />
  }
}

function Bars2({ shares, stage, color }: { shares: { name: string; plays: number }[]; stage: boolean; color: string }) {
  return (
    <ul className={cn('flex flex-col', stage ? 'gap-[1.6vh]' : 'gap-2.5')}>
      {shareBars(shares).map((g, i) => (
        <li key={g.name} className={cn('flex items-center gap-3', stage ? 'text-[clamp(1.1rem,2vw,2.2rem)]' : 'text-base')}>
          <motion.span
            initial={{ width: 0 }}
            animate={{ width: `${Math.max(g.fraction * 60, 4)}%` }}
            transition={{ delay: 0.2 + i * 0.08, duration: 0.9, ease: easeOutExpo }}
            className={cn('shrink-0 rounded-full', color, stage ? 'h-[3.4vh]' : 'h-6')}
          />
          <span className="truncate font-medium capitalize">{g.name}</span>
        </li>
      ))}
    </ul>
  )
}

function Outro({
  recap: r,
  roomId,
  roomName,
  from,
  to,
  stage,
  title,
  name,
}: {
  recap: Recap
  roomId: string
  roomName: string
  from: string
  to: string
  stage: boolean
  title: string
  name: (id: string) => string
}) {
  const [sharing, setSharing] = useState(false)
  const playlist = r.playlists[0]
  const date = new Date(from).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })

  const share = async () => {
    setSharing(true)
    try {
      const blob = await drawWrappedCard({ recap: r, roomId, roomName, title, date, nameOf: name })
      const how = await shareCard(blob, `syncphony-wrapped-${from.slice(0, 10)}.png`)
      if (how === 'downloaded') toast({ message: 'Saved the card' })
    } catch {
      toast({ message: "Couldn't make the card", tone: 'error' })
    } finally {
      setSharing(false)
    }
  }

  if (stage) {
    return (
      <>
        <Kicker stage className="text-amber-200">That&apos;s a wrap</Kicker>
        <Big stage>Thanks for the music.</Big>
        {playlist ? (
          <div className="flex items-center gap-[3vw]">
            <QrCode value={`${location.origin}/library/${playlist.id}`} label={`Open ${playlist.name}`} className="size-[22vh] rounded-[2vh] bg-white p-[1vh]" />
            <Line stage>
              Tonight&apos;s playlist is saved: <span className="font-semibold text-white">{playlist.name}</span>. Scan to play it again.
            </Line>
          </div>
        ) : (
          <Line stage>Save tonight&apos;s playlist from the recap on your phone.</Line>
        )}
      </>
    )
  }

  return (
    <>
      <Kicker stage={false} className="text-amber-200">That&apos;s a wrap</Kicker>
      <Big stage={false}>{title}</Big>
      <Line stage={false}>
        {r.stats.plays} {r.stats.plays === 1 ? 'song' : 'songs'}
        {r.stats.listeningMs >= 60_000 && `, ${formatListening(r.stats.listeningMs)}`}. Keep it, share it, play it again.
      </Line>
      {/* The page lets taps through to the tap layer; these take them. */}
      <div className="pointer-events-auto flex flex-col gap-2">
        <Button size="lg" onClick={() => void share()} disabled={sharing} className="bg-white text-black hover:bg-white/90">
          {sharing ? <LoaderCircle className="animate-spin" data-icon="inline-start" /> : <Share2 data-icon="inline-start" />}
          Share the card
        </Button>
        {playlist ? (
          <Button asChild size="lg" variant="glass" className="text-white">
            <Link to="/library/$playlistId" params={{ playlistId: playlist.id }}>
              <ListMusic data-icon="inline-start" />
              Open {playlist.name}
            </Link>
          </Button>
        ) : (
          <SaveNightButton night={{ roomId, roomName, from, to }} size="lg" variant="glass" className="text-white">
            <ListMusic data-icon="inline-start" />
            Save tonight&apos;s playlist
          </SaveNightButton>
        )}
      </div>
    </>
  )
}
