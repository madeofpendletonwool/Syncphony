import { useInfiniteQuery } from '@tanstack/react-query'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useRef, useState } from 'react'
import { chatQuery, liveChat, type ChatLook, type ChatMessage } from '@/lib/chat'
import { laneStyle } from '@/lib/lane'
import type { User } from '@/lib/now-playing'
import { useStore } from '@/lib/store'

/**
 * The room's chat on a big screen (MAD-845), in the room's chosen look:
 * floating across the top, a log in the corner, or nothing. The TV and the
 * app's visualizer both show it.
 */
export function BigScreenChat({ roomId, look = 'floating', users }: { roomId: string; look?: ChatLook; users: User[] | undefined }) {
  if (look === 'off') return null
  return look === 'log' ? <ChatLog roomId={roomId} users={users} /> : <FloatingChat roomId={roomId} users={users} />
}

/** How long a message takes to drift across, and how many drift at once. */
const DRIFT_S = 16
const MAX_ON_SCREEN = 4
const LANES = 2

type Floater = ChatMessage & { lane: number }

/**
 * Messages drifting across the top of the screen, right to left, like
 * reactions: clear of the lyrics and the now-playing title below. Two
 * lanes, a few at a time; a deleted message leaves at once.
 */
export function FloatingChat({ roomId, users }: { roomId: string; users: User[] | undefined }) {
  const all = useStore(liveChat)
  // Only what arrives from now on.
  const seen = useRef<Set<string> | null>(null)
  seen.current ??= new Set(all.map((m) => m.id))
  const lanes = useRef<number[]>(Array(LANES).fill(0))
  const [floaters, setFloaters] = useState<Floater[]>([])

  useEffect(() => {
    const fresh = all.filter((m) => !seen.current?.has(m.id) && m.roomId === roomId)
    if (fresh.length === 0) return
    const added = fresh.map((m) => {
      seen.current?.add(m.id)
      // The lane that's been free longest.
      const lane = lanes.current.indexOf(Math.min(...lanes.current))
      lanes.current[lane] = Date.now()
      return { ...m, lane }
    })
    setFloaters((f) => [...f, ...added].slice(-MAX_ON_SCREEN))
    for (const m of added) setTimeout(() => setFloaters((f) => f.filter((x) => x.id !== m.id)), DRIFT_S * 1000)
  }, [all, roomId])

  // A deleted message leaves the screen at once.
  const ids = new Set(all.map((m) => m.id))
  const shown = floaters.filter((f) => ids.has(f.id))

  return (
    <div aria-hidden className="pointer-events-none fixed inset-x-0 top-0 z-30 h-[16vh] overflow-hidden">
      <AnimatePresence>
        {shown.map((f) => (
          <motion.div
            key={f.id}
            initial={{ x: '100vw', opacity: 0 }}
            animate={{ x: '-110%', opacity: [0, 1, 1, 0] }}
            exit={{ opacity: 0, transition: { duration: 0.4 } }}
            transition={{ duration: DRIFT_S, ease: 'linear', opacity: { duration: DRIFT_S, times: [0, 0.06, 0.9, 1] } }}
            style={{ top: `${1.5 + f.lane * 6.5}vh` }}
            className="absolute left-0"
          >
            <Bubble m={f} user={users?.find((u) => u.id === f.userId)} />
          </motion.div>
        ))}
      </AnimatePresence>
    </div>
  )
}

/** Messages older than this leave the log. */
const LOG_FOR_MS = 15 * 60_000
const LOG_SHOWN = 3

/** The last few messages, in a column in the top right corner. */
export function ChatLog({ roomId, users }: { roomId: string; users: User[] | undefined }) {
  const chat = useInfiniteQuery(chatQuery(roomId))
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(t)
  }, [])
  const recent = (chat.data?.pages[0] ?? [])
    .slice(0, LOG_SHOWN)
    .filter((m) => now - Date.parse(m.createdAt) < LOG_FOR_MS)
    .reverse()

  return (
    <div aria-hidden className="pointer-events-none fixed top-[9vh] right-[4vw] z-30 flex w-[20vw] flex-col items-end gap-[1vh]">
      <AnimatePresence initial={false}>
        {recent.map((m) => (
          <motion.div
            key={m.id}
            layout
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.6 }}
          >
            <Bubble m={m} user={users?.find((u) => u.id === m.userId)} />
          </motion.div>
        ))}
      </AnimatePresence>
    </div>
  )
}

/** A glass speech bubble with the sender's name in their color. Long messages are clipped. */
function Bubble({ m, user }: { m: ChatMessage; user?: User }) {
  return (
    <div style={laneStyle(user?.color)} className="glass-strong flex max-w-[40vw] flex-col rounded-[2vh] rounded-bl-[0.6vh] px-[1.2vw] py-[0.9vh] shadow-float">
      <span className="text-[clamp(0.75rem,1vw,1.05rem)] font-semibold text-(--lane)">{user?.displayName ?? 'Someone'}</span>
      <span className="line-clamp-2 text-[clamp(1rem,1.6vw,1.75rem)] leading-snug break-words">{m.body}</span>
    </div>
  )
}
