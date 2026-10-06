import { Link } from '@tanstack/react-router'
import { AnimatePresence, motion } from 'motion/react'
import { Artwork } from '@/components/artwork'
import { Button } from '@/components/ui/button'
import { usePosition } from '@/hooks/use-position'
import { laneStyle } from '@/lib/lane'
import { spring } from '@/lib/motion'
import { usePlayer } from '@/lib/now-playing'
import { PlayPauseButton, SkipButton } from './player-controls'

/**
 * Always on screen above the nav. Shows the room's current song and expands
 * to the full now-playing view; when the room is quiet it invites you to
 * add something.
 */
export function MiniPlayer({ expanded, onExpand }: { expanded: boolean; onExpand: () => void }) {
  const { nowPlaying: np, commands } = usePlayer()
  const position = usePosition(np)

  if (!np) {
    return (
      <div className="glass-strong flex h-mini items-center gap-3 rounded-2xl px-2.5 shadow-float">
        <Artwork className="size-11 rounded-lg shadow-none" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">Nothing playing</p>
          <p className="truncate text-xs text-muted-foreground">The queue is waiting for its first song</p>
        </div>
        <Button asChild size="sm" variant="secondary">
          <Link to="/search">Add a song</Link>
        </Button>
      </div>
    )
  }

  const progress = np.track.durationMs > 0 ? position / np.track.durationMs : 0

  return (
    <div
      style={laneStyle(np.requester?.color)}
      className="glass-strong relative flex h-mini items-center gap-3 overflow-hidden rounded-2xl pr-1.5 pl-2.5 shadow-float"
    >
      {/* The whole bar opens now-playing; the buttons sit above it. */}
      <button
        type="button"
        onClick={onExpand}
        aria-label={`Open now playing: ${np.track.title}`}
        className="absolute inset-0 rounded-2xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      />
      {/* Hidden while expanded so the shared artwork has one owner. */}
      {expanded ? (
        <div className="size-11 shrink-0" />
      ) : (
        <Artwork src={np.artworkUrl} layoutId="now-playing-artwork" className="pointer-events-none size-11 rounded-lg" />
      )}
      <div className="pointer-events-none min-w-0 flex-1">
        <AnimatePresence mode="popLayout" initial={false}>
          <motion.div
            key={np.track.trackId}
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -8 }}
            transition={spring}
          >
            <p className="truncate text-sm font-medium">{np.track.title}</p>
            <p className="truncate text-xs text-muted-foreground">
              {np.track.artists.join(', ')}
              {np.requester && (
                <>
                  {' · '}
                  <span className="text-(--lane) dark:text-[color-mix(in_oklch,var(--lane),white_30%)]">
                    {np.requester.displayName}
                  </span>
                </>
              )}
              {np.autopilot && (
                <>
                  {' · '}
                  <span className="text-primary">Autopilot</span>
                </>
              )}
            </p>
          </motion.div>
        </AnimatePresence>
      </div>
      <PlayPauseButton size="icon" variant="ghost" paused={np.paused} onToggle={commands.toggle} className="relative" />
      <SkipButton size="icon" commands={commands} className="relative" />
      <div aria-hidden className="absolute inset-x-3 bottom-0 h-0.5 overflow-hidden rounded-full bg-foreground/10">
        <div
          className="h-full origin-left bg-(--lane,var(--primary)) transition-transform duration-300 ease-linear"
          style={{ transform: `scaleX(${progress})` }}
        />
      </div>
    </div>
  )
}
