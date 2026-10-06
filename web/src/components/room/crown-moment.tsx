import { useQuery } from '@tanstack/react-query'
import { Crown, Heart, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useMemo } from 'react'
import { Artwork } from '@/components/artwork'
import { Button } from '@/components/ui/button'
import { UserAvatar } from '@/components/user-avatar'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo } from '@/lib/motion'
import { crowning, dismissCrown, type Night } from '@/lib/nights'
import { queueArtworkUrl } from '@/lib/playback'
import { useStore } from '@/lib/store'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

/**
 * When the night ends, its song of the night takes the screen for a few
 * seconds: on phones as a card, on the big screen as the whole stage.
 */
export function CrownMoment({ roomId, variant = 'phone' }: { roomId?: string; variant?: 'phone' | 'stage' }) {
  const night = useStore(crowning)
  const show = night && (!roomId || night.roomId === roomId) ? night : null
  return <AnimatePresence>{show && <Moment key={show.id} night={show} variant={variant} />}</AnimatePresence>
}

function Moment({ night, variant }: { night: Night; variant: 'phone' | 'stage' }) {
  const users = useQuery(usersQuery)
  const song = night.songOfTheNight
  const by = song && !song.item.autopilot ? users.data?.find((u) => u.id === song.item.addedBy) : undefined
  const stage = variant === 'stage'
  const art = song ? queueArtworkUrl(night.roomId, song.item, stage ? 640 : 300) : undefined

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.6, ease: easeOutExpo }}
      role="status"
      aria-live="polite"
      className={cn(
        'fixed inset-0 z-[60] grid place-items-center overflow-hidden',
        stage ? 'bg-black/75 backdrop-blur-xl' : 'bg-black/60 px-gutter backdrop-blur-md',
      )}
      onClick={stage ? undefined : dismissCrown}
    >
      {song && <Rays stage={stage} />}
      <motion.div
        initial={{ opacity: 0, scale: 0.85, y: 30 }}
        animate={{ opacity: 1, scale: 1, y: 0 }}
        exit={{ opacity: 0, scale: 0.95 }}
        transition={{ duration: 0.9, ease: easeOutExpo, delay: 0.15 }}
        style={laneStyle(by?.color)}
        className={cn(
          'relative flex flex-col items-center text-center',
          stage ? 'gap-[3vh]' : 'glass-strong w-full max-w-sm gap-4 rounded-3xl p-6 shadow-float',
        )}
        onClick={(e) => e.stopPropagation()}
      >
        {!stage && (
          <Button size="icon-sm" variant="ghost" aria-label="Close" className="absolute top-3 right-3" onClick={dismissCrown}>
            <X />
          </Button>
        )}
        <motion.span
          initial={{ rotate: -20, scale: 0 }}
          animate={{ rotate: 0, scale: 1 }}
          transition={{ type: 'spring', stiffness: 260, damping: 14, delay: 0.5 }}
          className={cn('grid place-items-center rounded-full bg-amber-400/20 text-amber-400', stage ? 'size-[11vh]' : 'size-14')}
        >
          <Crown className={stage ? 'size-[6vh]' : 'size-7'} />
        </motion.span>
        <p className={cn('font-semibold tracking-[0.2em] text-amber-300 uppercase', stage ? 'text-[clamp(1rem,1.8vw,2rem)]' : 'text-caption')}>
          {song ? 'Song of the night' : "That's a wrap"}
        </p>

        {song ? (
          <>
            <Artwork
              src={art}
              className={cn(
                'shadow-[0_30px_100px_-20px_rgb(251_191_36/0.55)] ring-2 ring-amber-300/60',
                stage ? 'size-[38vh] rounded-[3vh]' : 'size-44 rounded-2xl',
              )}
            />
            <div className="min-w-0">
              <h2 className={cn('font-bold tracking-tight text-balance text-white', stage ? 'text-[clamp(2rem,4.5vw,5rem)] leading-tight' : 'text-title')}>
                {song.item.track.title}
              </h2>
              <p className={cn('text-white/70', stage ? 'text-[clamp(1.1rem,2vw,2.2rem)]' : '')}>{song.item.track.artists.join(', ')}</p>
            </div>
            <div className={cn('flex flex-wrap items-center justify-center text-white/85', stage ? 'gap-[2vw] text-[clamp(1rem,1.6vw,1.8rem)]' : 'gap-3 text-sm')}>
              {by && (
                <span className="flex items-center gap-2">
                  <UserAvatar user={by} className={cn('ring-2 ring-(--lane)', stage ? 'size-[5vh] text-[1.8vh]' : 'size-6 text-[0.6rem]')} />
                  Picked by <span className="font-semibold text-(--lane)">{by.displayName}</span>
                </span>
              )}
              <span className="flex items-center gap-1.5">
                <Heart className={cn('fill-rose-500 text-rose-500', stage ? 'size-[3vh]' : 'size-4')} />
                {song.hearts} {song.hearts === 1 ? 'heart' : 'hearts'}
              </span>
            </div>
          </>
        ) : (
          <p className={cn('max-w-[32ch] text-white/75', stage ? 'text-[clamp(1.1rem,2vw,2.2rem)]' : 'text-sm')}>
            {night.plays} {night.plays === 1 ? 'song' : 'songs'} tonight. Heart your favorites next time to crown a song of the night.
          </p>
        )}
      </motion.div>
    </motion.div>
  )
}

/** Slow, soft light rays behind the crowned song. */
function Rays({ stage }: { stage: boolean }) {
  const rays = useMemo(() => Array.from({ length: 12 }, (_, i) => i * 30), [])
  return (
    <motion.div
      aria-hidden
      initial={{ opacity: 0, rotate: 0 }}
      animate={{ opacity: 1, rotate: 360 }}
      transition={{ opacity: { duration: 1.5 }, rotate: { duration: 90, ease: 'linear', repeat: Infinity } }}
      className={cn('pointer-events-none absolute', stage ? 'size-[160vmax]' : 'size-[140vmax]')}
    >
      {rays.map((deg) => (
        <span
          key={deg}
          style={{ transform: `rotate(${deg}deg)` }}
          className="absolute top-1/2 left-1/2 h-1/2 w-[8%] origin-top -translate-x-1/2 bg-[linear-gradient(to_bottom,rgb(251_191_36/0.18),transparent_70%)]"
        />
      ))}
    </motion.div>
  )
}
