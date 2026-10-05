import { Outlet, useRouterState } from '@tanstack/react-router'
import { LayoutGroup, motion, MotionConfig } from 'motion/react'
import { useState } from 'react'
import { useAlbumAccent } from '@/hooks/use-album-accent'
import { easeOutExpo } from '@/lib/motion'
import { usePlayer } from '@/lib/now-playing'
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
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const [expanded, setExpanded] = useState(false)
  useAlbumAccent(nowPlaying?.artworkUrl)

  return (
    <MotionConfig reducedMotion="user">
      <LayoutGroup>
        <div className="relative isolate min-h-dvh">
          <AlbumBackdrop src={nowPlaying?.artworkUrl} />

          <motion.main
            key={pathname}
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.35, ease: easeOutExpo }}
            className="pt-safe mx-auto w-full max-w-2xl px-gutter pb-[calc(var(--spacing-nav)+var(--spacing-mini)+env(safe-area-inset-bottom)+2.5rem)]"
          >
            <Outlet />
          </motion.main>

          <div className="pointer-events-none fixed inset-x-0 bottom-0 z-40">
            <div className="pointer-events-auto mx-auto flex max-w-2xl flex-col gap-2 px-3 pt-2 pb-[calc(env(safe-area-inset-bottom)+0.75rem)]">
              <MiniPlayer expanded={expanded} onExpand={() => setExpanded(true)} />
              <BottomNav />
            </div>
          </div>

          <NowPlayingSheet open={expanded} onOpenChange={setExpanded} />
        </div>
      </LayoutGroup>
    </MotionConfig>
  )
}
