import { useQuery } from '@tanstack/react-query'
import { motion } from 'motion/react'
import { UserAvatar } from '@/components/user-avatar'
import type { Award } from '@/lib/games'
import { laneStyle } from '@/lib/lane'
import { fadeUp, stagger } from '@/lib/motion'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

const ICONS: Record<Award['kind'], string> = {
  deepest_cut: '💎',
  dance_floor_mvp: '🕺',
  vibe_killer: '🧊',
  trendsetter: '📈',
  time_traveler: '⏳',
  tempo_whiplash: '🎢',
  sample_snitch: '🔍',
  comeback: '🔁',
  opener: '🎬',
  closer: '🌙',
  trivia_champ: '🧠',
}

/**
 * A night's awards (MAD-787): who won what, for which song, and why. On
 * phones and in recaps as a list; on the big screen as a grid of cards.
 */
export function AwardsList({ awards, variant = 'list', className }: { awards: Award[]; variant?: 'list' | 'stage'; className?: string }) {
  const users = useQuery(usersQuery).data
  if (awards.length === 0) return null
  const stage = variant === 'stage'
  return (
    <motion.ul
      variants={stagger}
      initial="hidden"
      animate="show"
      className={cn(stage ? 'grid grid-cols-3 gap-[1.5vw]' : 'flex flex-col gap-1', className)}
    >
      {awards.map((a) => {
        const u = users?.find((x) => x.id === a.userId)
        return (
          <motion.li
            key={a.kind}
            variants={fadeUp}
            style={laneStyle(u?.color)}
            className={cn(
              'flex min-w-0 items-center text-left',
              stage ? 'glass gap-[1vw] rounded-[2.4vh] p-[1.6vh]' : 'gap-3 rounded-2xl px-2 py-2',
            )}
          >
            <span aria-hidden className={cn('shrink-0 leading-none', stage ? 'text-[5vh]' : 'text-2xl')}>
              {ICONS[a.kind]}
            </span>
            <div className="min-w-0 flex-1">
              <p className={cn('font-semibold', stage ? 'text-[clamp(1rem,1.5vw,1.6rem)]' : 'text-sm')}>{a.title}</p>
              <p className={cn('flex min-w-0 items-center gap-1.5 text-muted-foreground', stage ? 'text-[clamp(0.85rem,1.15vw,1.2rem)]' : 'text-caption')}>
                {u && <UserAvatar user={u} className={cn('ring-1 ring-(--lane)', stage ? 'size-[3vh] text-[1.1vh]' : 'size-4 text-[0.45rem]')} />}
                <span className="shrink-0 font-medium text-(--lane)">{u?.displayName ?? 'Someone'}</span>
                {a.item && <span className="truncate">· {a.item.track.title}</span>}
              </p>
              <p className={cn('line-clamp-2 text-muted-foreground', stage ? 'text-[clamp(0.8rem,1vw,1.1rem)]' : 'text-caption')}>{a.reason}</p>
            </div>
          </motion.li>
        )
      })}
    </motion.ul>
  )
}
