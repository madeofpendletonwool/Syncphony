import { useEffect, useState } from 'react'

/**
 * An image URL that changes only once the new image has loaded, so a swap
 * goes straight from one picture to the next instead of through a blank
 * frame. The first URL shows right away; one that fails to load comes
 * through anyway, for the img's own error handling.
 */
export function useLoadedSrc(src: string | undefined) {
  const [loaded, setLoaded] = useState(src)

  useEffect(() => {
    if (!src) return
    let stale = false
    const img = new Image()
    img.src = src
    void img
      .decode()
      .catch(() => {})
      .then(() => {
        if (!stale) setLoaded(src)
      })
    return () => {
      stale = true
    }
  }, [src])

  return src ? loaded : undefined
}
