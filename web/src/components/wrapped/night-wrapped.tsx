import { useQuery } from '@tanstack/react-query'
import { AnimatePresence } from 'motion/react'
import { useEffect, useRef } from 'react'
import { SaveNightSheet } from '@/components/playlists/save-night'
import { WrappedStory } from '@/components/wrapped/wrapped-story'
import { afterNight, crowning, type Night } from '@/lib/nights'
import { roomsQuery } from '@/lib/room'
import { useStore } from '@/lib/store'
import { nightRange } from '@/lib/wrapped'

/**
 * What follows a night's crowning: its Wrapped, or saving its playlist.
 * On phones, from the crowning's buttons. On the big screen, the Wrapped
 * plays by itself once the crowning is done.
 */
export function NightWrapped({ roomId, roomName, variant = 'phone' }: { roomId?: string; roomName?: string; variant?: 'phone' | 'stage' }) {
  const after = useStore(afterNight)
  const rooms = useQuery({ ...roomsQuery, enabled: variant === 'phone' })
  const stage = variant === 'stage'

  // The big screen: when a crowning (of a night that played something)
  // leaves the screen, the Wrapped comes on.
  const crowned = useStore(crowning)
  const last = useRef<Night | null>(null)
  useEffect(() => {
    if (!stage) return
    if (crowned) last.current = crowned
    else if (last.current) {
      const n = last.current
      last.current = null
      if (n.plays > 0 && (!roomId || n.roomId === roomId)) afterNight.set({ night: n, show: 'wrapped' })
    }
  }, [crowned, stage, roomId])

  const n = after && (!roomId || after.night.roomId === roomId) ? after.night : null
  const name = roomName ?? rooms.data?.find((r) => r.id === n?.roomId)?.name ?? 'The room'
  const range = n ? nightRange(n) : undefined
  const close = () => afterNight.set(null)

  return (
    <>
      <AnimatePresence>
        {n && range && after?.show === 'wrapped' && (
          <WrappedStory key={n.id} roomId={n.roomId} roomName={name} from={range.from} to={range.to} variant={variant} onClose={close} />
        )}
      </AnimatePresence>
      {!stage && n && range && (
        <SaveNightSheet
          key={n.id}
          night={{ roomId: n.roomId, roomName: name, ...range }}
          open={after?.show === 'save'}
          onOpenChange={(open) => !open && close()}
        />
      )}
    </>
  )
}
