import { Outlet, useRouterState } from '@tanstack/react-router'
import { WifiOff } from 'lucide-react'
import { AnimatePresence, LayoutGroup, motion } from 'motion/react'
import { useState, type ReactNode } from 'react'
import { useAlbumPalette } from '@/hooks/use-album-palette'
import { useBeatSync } from '@/hooks/use-beat-sync'
import { easeOutExpo } from '@/lib/motion'
import { usePlayer } from '@/lib/now-playing'
import { useOnline } from '@/lib/pwa'
import { Toaster } from '@/components/toaster'
import { AlbumBackdrop } from './album-backdrop'
import { BeatLab } from './beat-lab'
import { BottomNav } from './bottom-nav'
import { MiniPlayer } from './mini-player'
import { NowPlayingSheet } from './now-playing-sheet'

/**
 * Mobile-first app frame: page content, then a floating dock with the
 * mini-player over the bottom nav. The whole UI takes its colors from the
 * current song's artwork.
 */
export function AppShell() {
  const { nowPlaying } = usePlayer()
  const [expanded, setExpanded] = useState(false)
  useAlbumPalette(nowPlaying)
  useBeatSync(nowPlaying)

  return (
    <LayoutGroup>
      <div className="relative isolate min-h-dvh">
        {/* Its scene rests while now playing covers it. */}
        <AlbumBackdrop src={nowPlaying?.artworkUrl} scene={!expanded} />

        <main className="pt-safe mx-auto w-full max-w-2xl px-gutter pb-[calc(var(--spacing-nav)+var(--spacing-mini)+env(safe-area-inset-bottom)+2.5rem)]">
          <OfflineBanner />
          <Outlet />
        </main>

        <div className="pointer-events-none fixed inset-x-0 bottom-0 z-40">
          <div className="pointer-events-auto mx-auto flex max-w-2xl flex-col gap-2 px-3 pt-2 pb-[calc(env(safe-area-inset-bottom)+0.75rem)]">
            <Toaster />
            <MiniPlayer expanded={expanded} onExpand={() => setExpanded(true)} />
            <BottomNav />
          </div>
        </div>

        <NowPlayingSheet open={expanded} onOpenChange={setExpanded} />
        {!expanded && <BeatLab />}
      </div>
    </LayoutGroup>
  )
}

/** The app opens offline (the service worker keeps its shell), but the music needs the server. */
function OfflineBanner() {
  const online = useOnline()
  return (
    <AnimatePresence initial={false}>
      {!online && (
        <motion.p
          role="status"
          initial={{ opacity: 0, height: 0 }}
          animate={{ opacity: 1, height: 'auto' }}
          exit={{ opacity: 0, height: 0 }}
          className="overflow-hidden"
        >
          <span className="mt-4 flex items-center gap-2 rounded-2xl bg-muted px-4 py-3 text-sm text-muted-foreground">
            <WifiOff className="size-4 shrink-0" />
            You&apos;re offline. Syncphony will catch up when you&apos;re back.
          </span>
        </motion.p>
      )}
    </AnimatePresence>
  )
}

/**
 * Fades each page in. Wrap the leaf <Outlet />, never a layout: the key
 * remounts everything inside on navigation, and the signed-in layout holds
 * the room connection and the speaker.
 */
export function PageTransition({ children }: { children: ReactNode }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  return (
    <motion.div
      key={pathname}
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.35, ease: easeOutExpo }}
    >
      {children}
    </motion.div>
  )
}
