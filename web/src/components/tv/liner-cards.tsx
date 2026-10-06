import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useState } from 'react'
import { easeOutExpo } from '@/lib/motion'
import type { LinerCard } from '@/lib/liner-notes'

const CARD_MS = 9000

/** Liner notes as cards that take turns, for the big screen. */
export function LinerCards({ cards, className }: { cards: LinerCard[]; className?: string }) {
  const [i, setI] = useState(0)
  useEffect(() => {
    if (cards.length < 2) return
    const id = window.setInterval(() => setI((n) => n + 1), CARD_MS)
    return () => window.clearInterval(id)
  }, [cards.length])
  if (cards.length === 0) return null
  const card = cards[i % cards.length]
  return (
    <div className={className}>
      <AnimatePresence mode="wait">
        <motion.div
          key={`${i % cards.length}-${card.text}`}
          initial={{ opacity: 0, y: 24, filter: 'blur(8px)' }}
          animate={{ opacity: 1, y: 0, filter: 'blur(0px)' }}
          exit={{ opacity: 0, y: -16, filter: 'blur(8px)' }}
          transition={{ duration: 0.8, ease: easeOutExpo }}
          className="glass flex flex-col gap-[1.5vh] rounded-[3vh] p-[3.5vh]"
        >
          <p className="text-[clamp(0.8rem,1.1vw,1.15rem)] font-semibold tracking-[0.18em] text-(--pal-text) uppercase">
            {card.title}
          </p>
          <p className="text-[clamp(1.4rem,2.6vw,2.75rem)] leading-snug font-semibold text-balance">{card.text}</p>
          {cards.length > 1 && (
            <div className="mt-[1vh] flex gap-1.5" aria-hidden>
              {cards.map((c, n) => (
                <span
                  key={`${n}-${c.kind}`}
                  className={`h-1 rounded-full transition-all duration-700 ${n === i % cards.length ? 'w-8 bg-(--pal-text)' : 'w-2 bg-foreground/25'}`}
                />
              ))}
            </div>
          )}
        </motion.div>
      </AnimatePresence>
    </div>
  )
}
