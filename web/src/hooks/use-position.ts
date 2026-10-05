import { useEffect, useState } from 'react'
import { positionAt, type NowPlaying } from '@/lib/now-playing'

/** The playback position, re-rendering a few times a second while playing. */
export function usePosition(np: NowPlaying | null) {
  const [now, setNow] = useState(() => Date.now())
  const playing = np !== null && !np.paused

  useEffect(() => {
    if (!playing) return
    const id = window.setInterval(() => setNow(Date.now()), 250)
    return () => window.clearInterval(id)
  }, [playing])

  return np ? positionAt(np, Math.max(now, np.at)) : 0
}
