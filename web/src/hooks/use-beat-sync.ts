import { useEffect } from 'react'
import { beatSource } from '@/lib/beat'
import { loadBeatMap } from '@/lib/beat-map'
import type { NowPlaying } from '@/lib/now-playing'

/**
 * Points the beat engine at what's playing, and loads the song's beat map.
 * Mount it once where the now playing lives: the app shell, the big screen.
 */
export function useBeatSync(np: NowPlaying | null) {
  useEffect(() => {
    beatSource.set((s) => ({ ...s, np }))
  }, [np])

  const roomId = np?.roomId
  const itemId = np?.itemId
  useEffect(() => {
    if (!roomId || !itemId) {
      beatSource.set((s) => ({ ...s, map: null, mapKey: null }))
      return
    }
    let stale = false
    // mapKey says the map is known for this song, even when there's none.
    void loadBeatMap(roomId, itemId).then((map) => {
      if (!stale) beatSource.set((s) => ({ ...s, map, mapKey: itemId }))
    })
    return () => {
      stale = true
    }
  }, [roomId, itemId])

  // Nothing playing here any more (the shell unmounted, say).
  useEffect(() => () => beatSource.set({ np: null, map: null, mapKey: null }), [])
}
