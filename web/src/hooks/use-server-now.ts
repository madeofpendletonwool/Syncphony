import { useEffect, useState } from 'react'
import { serverNow } from '@/lib/clock'

/** The server's time, ticking a few times a second, for countdowns. */
export function useServerNow(every = 250) {
  const [now, setNow] = useState(() => serverNow())
  useEffect(() => {
    const t = setInterval(() => setNow(serverNow()), every)
    return () => clearInterval(t)
  }, [every])
  return now
}
