import { useEffect } from 'react'
import { DEFAULT_ACCENT } from '@/lib/color'
import type { NowPlaying } from '@/lib/now-playing'
import { applyPalette, fallbackPalette, loadPalette } from '@/lib/palette'

/**
 * Tints the whole app with the now-playing artwork's palette, easing back
 * to the default violet when nothing's playing. Mount it once (the shell
 * does).
 *
 * The palette usually arrives with the queue item. If the song started
 * before the server had worked it out, it's fetched; if the server can't
 * read the art (an SVG), the browser takes the accent from the image.
 */
export function useAlbumPalette(np: Pick<NowPlaying, 'roomId' | 'itemId' | 'artworkUrl' | 'palette'> | null) {
  const { roomId, itemId, artworkUrl, palette } = np ?? {}
  // Palettes arrive as fresh objects with every event; compare by value.
  const key = palette ? JSON.stringify(palette) : ''
  useEffect(() => {
    if (palette) {
      applyPalette(palette)
      return
    }
    if (!artworkUrl) {
      applyPalette(fallbackPalette({ ...DEFAULT_ACCENT }))
      return
    }
    let stale = false
    void loadPalette(roomId, itemId, artworkUrl).then((p) => {
      if (!stale) applyPalette(p)
    })
    return () => {
      stale = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- palette is compared by key
  }, [roomId, itemId, artworkUrl, key])
}
