import { ChevronDown } from 'lucide-react'
import { AnimatePresence, motion, useDragControls } from 'motion/react'
import { Dialog } from 'radix-ui'
import { useState } from 'react'
import { Artwork } from '@/components/artwork'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Slider } from '@/components/ui/slider'
import { UserAvatar } from '@/components/user-avatar'
import { usePosition } from '@/hooks/use-position'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo, spring } from '@/lib/motion'
import { formatDuration, usePlayer } from '@/lib/now-playing'
import { AlbumBackdrop } from './album-backdrop'
import { TransportControls } from './player-controls'

/** Full-screen now playing. Swipe down or press Escape to close. */
export function NowPlayingSheet({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { nowPlaying: np, commands } = usePlayer()
  const position = usePosition(np)
  const drag = useDragControls()
  // While scrubbing, show the thumb where the finger is, not the live position.
  const [scrub, setScrub] = useState<number>()

  return (
    <Dialog.Root open={open && np !== null} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && np && (
          <Dialog.Portal forceMount>
            <Dialog.Overlay asChild forceMount>
              <motion.div
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                className="fixed inset-0 z-50 bg-black/40"
              />
            </Dialog.Overlay>
            <Dialog.Content asChild forceMount aria-describedby={undefined}>
              <motion.div
                initial={{ y: '100%' }}
                animate={{ y: 0 }}
                exit={{ y: '100%' }}
                transition={{ duration: 0.5, ease: easeOutExpo }}
                drag="y"
                dragListener={false}
                dragControls={drag}
                dragConstraints={{ top: 0, bottom: 0 }}
                dragElastic={{ top: 0, bottom: 0.6 }}
                onDragEnd={(_, info) => {
                  if (info.offset.y > 120 || info.velocity.y > 600) onOpenChange(false)
                }}
                style={laneStyle(np.requester?.color)}
                className="fixed inset-0 isolate z-50 flex flex-col overflow-hidden bg-background outline-none"
              >
                <AlbumBackdrop src={np.artworkUrl} className="absolute" />
                <div
                  onPointerDown={(e) => drag.start(e)}
                  className="flex touch-none items-center justify-between px-gutter pt-[calc(env(safe-area-inset-top)+0.75rem)] pb-3"
                >
                  <Dialog.Close asChild>
                    <Button size="icon" variant="glass" aria-label="Close now playing">
                      <ChevronDown />
                    </Button>
                  </Dialog.Close>
                  <span aria-hidden className="h-1.5 w-10 rounded-full bg-foreground/25" />
                  <span className="size-10" />
                </div>

                <div
                  onPointerDown={(e) => drag.start(e)}
                  className="flex flex-1 touch-none items-center justify-center px-8 py-4"
                >
                  <Artwork
                    src={np.artworkUrl}
                    alt={np.track.album ? `${np.track.album} cover` : ''}
                    layoutId="now-playing-artwork"
                    className="w-full max-w-[min(26rem,52dvh)] rounded-3xl shadow-[0_30px_80px_-20px_var(--glow)]"
                  />
                </div>

                <div className="pb-safe mx-auto w-full max-w-xl px-gutter">
                  <motion.div
                    key={np.track.trackId}
                    initial={{ opacity: 0, y: 10 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={spring}
                    className="flex items-end justify-between gap-4"
                  >
                    <div className="min-w-0">
                      <Dialog.Title className="truncate text-title">{np.track.title}</Dialog.Title>
                      <p className="truncate text-headline font-normal text-muted-foreground">
                        {np.track.artists.join(', ')}
                      </p>
                    </div>
                    {np.requester && (
                      <Badge variant="lane" style={laneStyle(np.requester.color)} className="gap-1.5 py-1 pl-1">
                        <UserAvatar user={np.requester} className="size-5 text-[0.6rem]" />
                        {np.requester.displayName}
                      </Badge>
                    )}
                  </motion.div>

                  <div className="mt-6">
                    <Slider
                      aria-label="Seek"
                      max={np.track.durationMs}
                      step={1000}
                      value={[scrub ?? position]}
                      disabled={!commands.seek}
                      onValueChange={([v]) => setScrub(v)}
                      onValueCommit={([v]) => {
                        commands.seek?.(v)
                        setScrub(undefined)
                      }}
                    />
                    <div className="flex justify-between text-caption text-muted-foreground tabular-nums">
                      <span>{formatDuration(scrub ?? position)}</span>
                      <span>-{formatDuration(np.track.durationMs - (scrub ?? position))}</span>
                    </div>
                  </div>

                  <div className="py-8">
                    <TransportControls paused={np.paused} commands={commands} />
                  </div>
                </div>
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}
