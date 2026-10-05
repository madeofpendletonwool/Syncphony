import { Outlet, useRouterState } from '@tanstack/react-router'
import { LayoutGroup, motion } from 'motion/react'
import { useState, type ReactNode } from 'react'
import { useAlbumAccent } from '@/hooks/use-album-accent'
import { easeOutExpo } from '@/lib/motion'
import { usePlayer } from '@/lib/now-playing'
import { Toaster } from '@/components/toaster'
import { AlbumBackdrop } from './album-backdrop'
import { BottomNav } from './bottom-nav'
import { MiniPlayer } from './mini-player'
import { NowPlayingSheet } from './now-playing-sheet'

/**
 * Mobile-first app frame: page content, then a floating dock with the
 * mini-player over the bottom nav. The whole UI takes its accent from the
 * current song's artwork.
 */
export function AppShell() {
  const { nowPlaying } = usePlayer()
  const [expanded, setExpanded] = useState(false)
  useAlbumAccent(nowPlaying?.artworkUrl)

  return (
    <LayoutGroup>
      <div className="relative isolate min-h-dvh">
        <AlbumBackdrop src={nowPlaying?.artworkUrl} />

        <main className="pt-safe mx-auto w-full max-w-2xl px-gutter pb-[calc(var(--spacing-nav)+var(--spacing-mini)+env(safe-area-inset-bottom)+2.5rem)]">
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
      </div>
    </LayoutGroup>
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
