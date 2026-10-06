import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useRef, useState } from 'react'
import type { User } from '@/lib/now-playing'
import { reactions, type Reaction } from '@/lib/reactions'
import { useStore } from '@/lib/store'

type Floater = Reaction & { x: number; sway: number; name?: string; color?: string }

const RISE_S = 5.5
const MAX_ON_SCREEN = 30

/** Emoji from phones floating up the big screen, each with who sent it. */
export function FloatingReactions({ users }: { users: User[] | undefined }) {
  const all = useStore(reactions)
  const seen = useRef(new Set<string>())
  const [floaters, setFloaters] = useState<Floater[]>([])

  useEffect(() => {
    const fresh = all.filter((r) => !seen.current.has(r.id))
    if (fresh.length === 0) return
    for (const r of fresh) seen.current.add(r.id)
    const added = fresh.map((r) => {
      const u = users?.find((x) => x.id === r.userId)
      return { ...r, x: 6 + Math.random() * 88, sway: (Math.random() - 0.5) * 12, name: u?.displayName, color: u?.color }
    })
    setFloaters((f) => [...f, ...added].slice(-MAX_ON_SCREEN))
    for (const r of added) setTimeout(() => setFloaters((f) => f.filter((x) => x.id !== r.id)), RISE_S * 1000)
  }, [all, users])

  return (
    <div aria-hidden className="pointer-events-none fixed inset-0 z-30 overflow-hidden">
      <AnimatePresence>
        {floaters.map((f) => (
          <motion.div
            key={f.id}
            initial={{ y: '105vh', x: 0, opacity: 0, scale: 0.6 }}
            animate={{ y: '-15vh', x: [`0vw`, `${f.sway}vw`, `${-f.sway / 2}vw`], opacity: [0, 1, 1, 0], scale: [0.6, 1.15, 1, 0.9] }}
            exit={{ opacity: 0 }}
            transition={{ duration: RISE_S, ease: 'easeOut', times: [0, 0.1, 0.75, 1] }}
            style={{ left: `${f.x}vw` }}
            className="absolute flex flex-col items-center gap-1"
          >
            <span className="text-[clamp(2.5rem,5vw,5rem)] drop-shadow-[0_8px_24px_rgba(0,0,0,0.45)]">{f.emoji}</span>
            {f.name && (
              <span
                style={{ backgroundColor: f.color }}
                className="rounded-full px-2.5 py-0.5 text-[clamp(0.7rem,0.9vw,0.95rem)] font-semibold text-white shadow-lg"
              >
                {f.name}
              </span>
            )}
          </motion.div>
        ))}
      </AnimatePresence>
    </div>
  )
}
