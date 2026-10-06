import { useQueryClient } from '@tanstack/react-query'
import { MonitorPlay } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useState } from 'react'
import { ApiError } from '@/api/errors'
import { AlbumBackdrop } from '@/components/shell/album-backdrop'
import { beginPairing, displayMeQuery, formatPairingCode, pollPairing } from '@/lib/displays'
import { easeOutExpo } from '@/lib/motion'
import { QrCode } from './qr-code'

const POLL_MS = 2000

/**
 * A screen that isn't paired yet: a big code to type in from a phone in
 * the room. Codes last ten minutes; a fresh one replaces each that lapses.
 */
export function TvPairing() {
  const queryClient = useQueryClient()
  const [code, setCode] = useState<string>()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let stopped = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const start = async () => {
      try {
        const p = await beginPairing()
        if (stopped) return
        setCode(p.code)
        setFailed(false)
        timer = setTimeout(poll, POLL_MS)
      } catch {
        if (stopped) return
        setFailed(true)
        timer = setTimeout(start, 10_000)
      }
    }
    const poll = async () => {
      try {
        const st = await pollPairing()
        if (stopped) return
        if (st.status === 'paired') {
          await queryClient.invalidateQueries({ queryKey: displayMeQuery.queryKey })
          return
        }
        timer = setTimeout(poll, POLL_MS)
      } catch (e) {
        if (stopped) return
        // Expired: start over with a new code. Anything else: keep trying.
        if (e instanceof ApiError && e.status === 410) void start()
        else timer = setTimeout(poll, POLL_MS * 3)
      }
    }
    void start()
    return () => {
      stopped = true
      clearTimeout(timer)
    }
  }, [queryClient])

  return (
    <div className="relative isolate grid min-h-dvh cursor-none place-items-center overflow-hidden px-[6vw] select-none">
      <AlbumBackdrop />
      <div className="burn-in-drift flex flex-col items-center gap-[4vh] text-center">
        <span className="grid size-[9vh] place-items-center rounded-[2.5vh] bg-primary/15 text-primary">
          <MonitorPlay className="size-[5vh]" />
        </span>
        <h1 className="text-[clamp(2rem,4.5vw,4.5rem)] leading-tight font-bold tracking-tight">Show a room on this screen</h1>
        <p className="max-w-[48ch] text-[clamp(1rem,1.8vw,1.75rem)] text-muted-foreground">
          On your phone, open the room, tap its name, choose <span className="font-semibold text-foreground">Big screen</span>, and type in:
        </p>
        <AnimatePresence mode="wait">
          <motion.p
            key={code ?? (failed ? 'failed' : 'loading')}
            initial={{ opacity: 0, scale: 0.96 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.96 }}
            transition={{ duration: 0.5, ease: easeOutExpo }}
            aria-live="polite"
            className="glass rounded-[3vh] px-[4vw] py-[2.5vh] font-mono text-[clamp(3rem,10vw,10rem)] leading-none font-bold tracking-[0.12em] text-(--pal-text) tabular-nums"
          >
            {code ? formatPairingCode(code) : failed ? '— — —' : '· · ·'}
          </motion.p>
        </AnimatePresence>
        <p className="text-[clamp(0.9rem,1.4vw,1.25rem)] text-muted-foreground">
          {failed ? "Can't reach Syncphony. Trying again…" : 'Waiting for someone to pair it…'}
        </p>
      </div>
      <div className="glass absolute right-[3vw] bottom-[4vh] flex items-center gap-4 rounded-3xl p-4">
        <div className="rounded-xl bg-white p-1.5 text-black">
          <QrCode value={`${location.origin}/room`} label="Open Syncphony" className="size-[11vh]" />
        </div>
        <p className="max-w-[14ch] text-left text-[clamp(0.85rem,1.2vw,1.1rem)] text-muted-foreground">Scan to open Syncphony</p>
      </div>
    </div>
  )
}
