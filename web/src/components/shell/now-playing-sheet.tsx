import { ChevronDown, ListMusic, Mic2 } from 'lucide-react'
import { AnimatePresence, motion, useDragControls } from 'motion/react'
import { Dialog } from 'radix-ui'
import { useRef, useState } from 'react'
import { Artwork } from '@/components/artwork'
import { LyricsView } from '@/components/lyrics/lyrics-view'
import { AutopilotBadge, AutopilotWhy } from '@/components/room/autopilot-badge'
import { SourceTag } from '@/components/service-tag'
import { AlbumLink, ArtistLinks } from '@/components/track-credits'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Slider } from '@/components/ui/slider'
import { UserAvatar } from '@/components/user-avatar'
import { useBeat } from '@/hooks/use-beat'
import { usePosition } from '@/hooks/use-position'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo, spring } from '@/lib/motion'
import { formatDuration, usePlayer } from '@/lib/now-playing'
import { AlbumBackdrop } from './album-backdrop'
import { TransportControls } from './player-controls'
import { BeatLab } from './beat-lab'
import { SheetQueue } from './sheet-queue'
import { Waveform } from './waveform'

/**
 * Full-screen now playing, with the room's queue below (beside, on wide
 * screens). Swipe down or press Escape to close.
 */
export function NowPlayingSheet({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { nowPlaying: np, commands } = usePlayer()
  const position = usePosition(np)
  const drag = useDragControls()
  // While scrubbing, show the thumb where the finger is, not the live position.
  const [scrub, setScrub] = useState<number>()
  const queueRef = useRef<HTMLElement>(null)
  // Lyrics take the artwork's place.
  const [lyrics, setLyrics] = useState(false)
  const close = () => onOpenChange(false)
  const beat = useBeat<HTMLDivElement>()

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
                  className="relative flex touch-none items-center justify-between px-gutter pt-[calc(env(safe-area-inset-top)+0.75rem)] pb-3"
                >
                  <Dialog.Close asChild>
                    <Button size="icon" variant="glass" aria-label="Close now playing">
                      <ChevronDown />
                    </Button>
                  </Dialog.Close>
                  <span aria-hidden className="absolute left-1/2 h-1.5 w-10 -translate-x-1/2 rounded-full bg-foreground/25" />
                  {np.roomId ? (
                    <div className="flex gap-2">
                      <BeatLab inline />
                      <Button
                        size="icon"
                        variant={lyrics ? 'default' : 'glass'}
                        aria-label="Lyrics"
                        aria-pressed={lyrics}
                        onClick={() => setLyrics((l) => !l)}
                        onPointerDown={(e) => e.stopPropagation()}
                      >
                        <Mic2 />
                      </Button>
                    </div>
                  ) : (
                    <span className="size-10" />
                  )}
                </div>

                <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain lg:grid lg:grid-cols-[minmax(0,1fr)_minmax(0,26rem)] lg:overflow-hidden">
                  <div className="flex min-h-full flex-col lg:min-h-0 lg:overflow-y-auto">
                    {lyrics ? (
                      <div className="mx-auto flex h-[min(34rem,52dvh)] w-full max-w-xl flex-col px-gutter py-2">
                        <LyricsView np={np} variant="sheet" onSeek={commands.seek} className="h-full" />
                      </div>
                    ) : (
                      <div
                        onPointerDown={(e) => drag.start(e)}
                        className="flex flex-1 touch-none items-center justify-center px-8 py-4"
                      >
                        {/* Lifts on the one, with a bloom of the art's color behind it. */}
                        <div ref={beat} className="beat-lift relative w-full max-w-[min(26rem,46dvh)]">
                          <div aria-hidden className="beat-bloom pointer-events-none absolute -inset-[12%] -z-10 rounded-full blur-2xl" />
                          <Artwork
                            src={np.artworkUrl}
                            alt={np.track.album ? `${np.track.album} cover` : ''}
                            layoutId="now-playing-artwork"
                            className="w-full rounded-3xl shadow-[0_30px_80px_-20px_var(--glow)]"
                          />
                        </div>
                      </div>
                    )}

                    <div className="mx-auto w-full max-w-xl px-gutter">
                      <motion.div
                        key={np.track.trackId}
                        initial={{ opacity: 0, y: 10 }}
                        animate={{ opacity: 1, y: 0 }}
                        transition={spring}
                        className="min-w-0"
                      >
                        <Dialog.Title className="line-clamp-2 text-title">{np.track.title}</Dialog.Title>
                        <ArtistLinks
                          track={np.track}
                          onNavigate={close}
                          className="line-clamp-1 text-headline font-normal text-muted-foreground"
                        />
                        <AlbumLink track={np.track} onNavigate={close} className="line-clamp-1 text-sm text-muted-foreground/80" />
                        <div className="mt-3 flex flex-wrap items-center gap-1.5">
                          {np.requester && (
                            <Badge variant="lane" style={laneStyle(np.requester.color)} className="gap-1.5 py-1 pl-1">
                              <UserAvatar user={np.requester} className="size-5 text-[0.6rem]" />
                              Added by {np.requester.displayName}
                            </Badge>
                          )}
                          {np.autopilot && <AutopilotBadge pick={np.autopilot} />}
                          <SourceTag provider={np.track.provider} via={np.via} className="py-1" />
                        </div>
                        {np.autopilot && <AutopilotWhy pick={np.autopilot} className="mt-2 text-sm" />}
                      </motion.div>

                      <div className="mt-5">
                        <Waveform
                          itemId={np.itemId}
                          positionMs={scrub ?? position}
                          durationMs={np.track.durationMs}
                          className="mb-1"
                        />
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

                      <div className="py-6">
                        <TransportControls paused={np.paused} commands={commands} />
                      </div>

                      {np.roomId && (
                        <button
                          type="button"
                          onClick={() => queueRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })}
                          className="mx-auto mb-[calc(env(safe-area-inset-bottom)+0.75rem)] flex items-center gap-1.5 rounded-full px-3 py-1.5 text-sm font-medium text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 lg:hidden"
                        >
                          <ListMusic className="size-4" />
                          Queue
                        </button>
                      )}
                    </div>
                  </div>

                  {np.roomId && (
                    <SheetQueue
                      ref={queueRef}
                      roomId={np.roomId}
                      itemId={np.itemId}
                      className="mx-auto w-full max-w-xl scroll-mt-2 lg:max-w-none lg:overflow-y-auto lg:pt-4"
                    />
                  )}
                </div>
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}
