import { Pause, Play, SkipBack, SkipForward } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Button } from '@/components/ui/button'
import type { PlayerCommands } from '@/lib/now-playing'

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
      <Button size="icon-lg" variant="ghost" aria-label="Next" disabled={!commands.next} onClick={commands.next}>
        <SkipForward className="fill-current" />
      </Button>
    </div>
  )
}
