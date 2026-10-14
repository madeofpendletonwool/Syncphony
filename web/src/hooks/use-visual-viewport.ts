import { useEffect, useState } from 'react'

type Viewport = { height: number; offsetTop: number }

function read(): Viewport {
  const vv = typeof window === 'undefined' ? undefined : window.visualViewport
  return vv ? { height: vv.height, offsetTop: vv.offsetTop } : { height: typeof window === 'undefined' ? 0 : window.innerHeight, offsetTop: 0 }
}

/**
 * The part of the page that's actually visible. On a phone the keyboard
 * shrinks it without resizing the layout, so anything pinned to the
 * bottom (a composer) has to follow it to stay above the keyboard.
 */
export function useVisualViewport(enabled = true) {
  const [vp, setVp] = useState(read)
  useEffect(() => {
    const vv = window.visualViewport
    if (!enabled || !vv) return
    const update = () => setVp(read())
    update()
    vv.addEventListener('resize', update)
    vv.addEventListener('scroll', update)
    return () => {
      vv.removeEventListener('resize', update)
      vv.removeEventListener('scroll', update)
    }
  }, [enabled])
  return vp
}
