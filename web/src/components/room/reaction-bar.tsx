import { AnimatePresence, motion } from 'motion/react'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { tap } from '@/lib/haptics'
import { fadeUp } from '@/lib/motion'
import { REACTIONS, sendReaction, type ReactionEmoji } from '@/lib/reactions'
import { toast } from '@/lib/toast'

let nextBurst = 0

/** Emoji anyone can fire at the room's big screen. */
export function ReactionBar({ roomId }: { roomId: string }) {
  // Little copies that float off the tapped button.
  const [bursts, setBursts] = useState<{ id: number; emoji: ReactionEmoji }[]>([])

  const send = (emoji: ReactionEmoji) => {
    tap()
    const id = nextBurst++
    setBursts((b) => [...b.slice(-12), { id, emoji }])
    setTimeout(() => setBursts((b) => b.filter((x) => x.id !== id)), 900)
    sendReaction(roomId, emoji).catch((e: unknown) => toast({ message: errorMessage(e), tone: 'error' }))
  }

  return (
    <motion.div variants={fadeUp} className="glass flex items-center justify-between gap-1 rounded-full p-1.5" role="group" aria-label="React on the big screen">
      {REACTIONS.map((emoji) => (
        <motion.button
          key={emoji}
          type="button"
          whileTap={{ scale: 1.35 }}
          onClick={() => send(emoji)}
          aria-label={`Send ${emoji}`}
          className="relative grid size-10 place-items-center rounded-full text-xl outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          {emoji}
          <AnimatePresence>
            {bursts
              .filter((b) => b.emoji === emoji)
              .map((b) => (
                <motion.span
                  key={b.id}
                  aria-hidden
                  initial={{ y: 0, opacity: 1, scale: 1 }}
                  animate={{ y: -56, opacity: 0, scale: 1.4 }}
                  transition={{ duration: 0.9, ease: 'easeOut' }}
                  className="pointer-events-none absolute"
                >
                  {emoji}
                </motion.span>
              ))}
          </AnimatePresence>
        </motion.button>
      ))}
    </motion.div>
  )
}
