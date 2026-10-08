import { useQuery } from '@tanstack/react-query'
import { Check, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState } from 'react'
import { Artwork } from '@/components/artwork'
import { Button } from '@/components/ui/button'
import { usePlaybackCommand } from '@/hooks/use-play-now'
import { useMe } from '@/lib/auth'
import { spring } from '@/lib/motion'
import { playbackQuery, queueArtworkUrl } from '@/lib/playback'
import { queueQuery, useCurrentRoom } from '@/lib/room'
import { usersQuery } from '@/lib/users'

/**
 * Someone asked to play a song now, in a room that votes on skips: the
 * rest of the room says yes here, on any screen. Whoever asked sees the
 * tally and can take it back.
 */
export function PlayNowRequest() {
  const me = useMe()
  const { room } = useCurrentRoom()
  const roomId = room?.id ?? ''
  const playback = useQuery({ ...playbackQuery(roomId), enabled: false })
  const queue = useQuery({ ...queueQuery(roomId), enabled: false })
  const users = useQuery(usersQuery)
  const command = usePlaybackCommand(roomId)
  // "Not now" hides a request here; it lapses on its own.
  const [hidden, setHidden] = useState<string>()

  const req = room ? playback.data?.playNow : undefined
  const item = req && queue.data?.items.find((i) => i.id === req.itemId)
  const mine = req?.by === me.id
  const voted = !!req?.voters.includes(me.id)
  const mayVote = !me.guest || !!room?.guests.canVote
  const show = req && item && mayVote && hidden !== `${req.itemId}:${req.by}`
  const by = users.data?.find((u) => u.id === req?.by)?.displayName ?? 'Someone'

  return (
    <AnimatePresence initial={false}>
      {show && (
        <motion.div
          key={req.itemId}
          initial={{ opacity: 0, y: 16, scale: 0.96 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={{ opacity: 0, y: 8, scale: 0.96 }}
          transition={spring}
          role="status"
          className="glass-strong flex items-center gap-3 rounded-2xl p-2 pl-2.5 shadow-float"
        >
          <Artwork src={queueArtworkUrl(roomId, item, 120)} className="size-10 rounded-lg shadow-none" />
          <div className="min-w-0 flex-1 text-sm">
            <p className="truncate">
              {mine ? 'You asked to play ' : `${by} wants to play `}
              <span className="font-medium">{item.track.title}</span>
            </p>
            <p className="text-caption text-muted-foreground tabular-nums">
              {req.voters.length} of {req.needed} agree
            </p>
          </div>
          {mine ? (
            <Button size="sm" variant="ghost" onClick={() => void command({ action: 'unvote_play_now', itemId: req.itemId })}>
              Cancel
            </Button>
          ) : (
            <>
              <Button
                size="icon"
                variant="ghost"
                aria-label="Not now"
                title="Not now"
                onClick={() => {
                  if (voted) void command({ action: 'unvote_play_now', itemId: req.itemId })
                  setHidden(`${req.itemId}:${req.by}`)
                }}
              >
                <X />
              </Button>
              <Button
                size="sm"
                variant={voted ? 'glass' : 'default'}
                aria-pressed={voted}
                onClick={() => void command({ action: voted ? 'unvote_play_now' : 'vote_play_now', itemId: req.itemId })}
              >
                <Check data-icon="inline-start" />
                {voted ? 'Agreed' : 'Play it'}
              </Button>
            </>
          )}
        </motion.div>
      )}
    </AnimatePresence>
  )
}
