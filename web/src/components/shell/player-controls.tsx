import { Pause, Play, SkipBack, SkipForward } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Button } from '@/components/ui/button'
import type { PlayerCommands } from '@/lib/now-playing'
import { cn } from '@/lib/utils'

/** A play/pause button whose icon morphs between states. */
export function PlayPauseButton({
  paused,
  onToggle,
  size,
  variant,
  className,
}: {
  paused: boolean
  onToggle?: () => void
  size: 'icon' | 'icon-xl'
  variant?: 'default' | 'ghost'
  className?: string
}) {
  const Icon = paused ? Play : Pause
  return (
    <Button
      size={size}
      variant={variant}
      className={className}
      aria-label={paused ? 'Play' : 'Pause'}
      disabled={!onToggle}
      onClick={(e) => {
        e.stopPropagation()
        onToggle?.()
      }}
    >
      <AnimatePresence initial={false} mode="popLayout">
        <motion.span
          key={paused ? 'play' : 'pause'}
          initial={{ scale: 0.4, opacity: 0, rotate: -30 }}
          animate={{ scale: 1, opacity: 1, rotate: 0 }}
          exit={{ scale: 0.4, opacity: 0, rotate: 30 }}
          transition={{ duration: 0.18 }}
          className="grid place-items-center"
        >
          <Icon className="fill-current" />
        </motion.span>
      </AnimatePresence>
    </Button>
  )
}

export function TransportControls({ paused, commands }: { paused: boolean; commands: PlayerCommands }) {
  return (
    <div className="flex items-center justify-center gap-6">
      <Button size="icon-lg" variant="ghost" aria-label="Previous" disabled={!commands.previous} onClick={commands.previous}>
        <SkipBack className="fill-current" />
      </Button>
      <PlayPauseButton size="icon-xl" paused={paused} onToggle={commands.toggle} />
      <SkipButton size="icon-lg" commands={commands} />
    </div>
  )
}

/**
 * Next, or in a room that votes on skips, your vote: pressed once you've
 * voted, with the tally beside the icon.
 */
export function SkipButton({
  commands,
  size,
  className,
}: {
  commands: PlayerCommands
  size: 'icon' | 'icon-lg'
  className?: string
}) {
  const vote = !commands.next ? commands.vote : undefined
  if (!vote) {
    return (
      <Button size={size} variant="ghost" aria-label="Next" disabled={!commands.next} onClick={commands.next} className={className}>
        <SkipForward className="fill-current" />
      </Button>
    )
  }
  const tally = `${vote.count} of ${vote.needed}`
  return (
    <Button
      size={size}
      variant="ghost"
      aria-pressed={vote.voted}
      aria-label={vote.voted ? `Take back your vote to skip (${tally} votes)` : `Vote to skip (${tally} votes)`}
      title={vote.voted ? 'Take back your vote' : 'Vote to skip'}
      onClick={(e) => {
        e.stopPropagation()
        vote.toggle()
      }}
      className={cn('relative aria-pressed:text-primary', className)}
    >
      <SkipForward className={cn(vote.voted ? 'fill-current' : 'fill-none')} />
      <AnimatePresence initial={false} mode="popLayout">
        <motion.span
          key={tally}
          initial={{ scale: 0.6, opacity: 0 }}
          animate={{ scale: 1, opacity: 1 }}
          exit={{ scale: 0.6, opacity: 0 }}
          transition={{ duration: 0.15 }}
          aria-hidden
          className={cn(
            'absolute -right-1 -bottom-0.5 rounded-full px-1.5 py-px text-[0.625rem] leading-4 font-semibold tabular-nums ring-2 ring-background',
            vote.voted ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground',
          )}
        >
          {vote.count}/{vote.needed}
        </motion.span>
      </AnimatePresence>
    </Button>
  )
}
