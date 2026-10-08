import { useEffect } from 'react'

/** Keeps the screen from sleeping while it's on show. */
export function useWakeLock() {
  useEffect(() => {
    let lock: WakeLockSentinel | undefined
    let stopped = false
    const request = async () => {
      if (document.visibilityState !== 'visible' || !('wakeLock' in navigator)) return
      try {
        lock = await navigator.wakeLock.request('screen')
        if (stopped) void lock.release()
      } catch {
        // Not allowed (battery saver, or no user gesture yet): the screen may dim.
      }
    }
    void request()
    document.addEventListener('visibilitychange', request)
    return () => {
      stopped = true
      document.removeEventListener('visibilitychange', request)
      void lock?.release()
    }
  }, [])
}
