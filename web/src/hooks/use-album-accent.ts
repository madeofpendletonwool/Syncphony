import { useEffect } from 'react'
import { applyAccent, extractAccent } from '@/lib/accent'

/**
 * Tints the whole app with the given artwork's dominant color, easing back
 * to the default accent when there's no artwork. Mount it once, for the
 * now-playing artwork (the shell does this).
 */
export function useAlbumAccent(artworkUrl: string | undefined) {
  useEffect(() => {
    if (!artworkUrl) {
      applyAccent(null)
      return
    }
    let stale = false
    void extractAccent(artworkUrl).then((accent) => {
      if (!stale) applyAccent(accent)
    })
    return () => {
      stale = true
    }
  }, [artworkUrl])
}
