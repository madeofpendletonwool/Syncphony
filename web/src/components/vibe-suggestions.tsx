import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { motion } from 'motion/react'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { Notice } from '@/components/notice'
import { TrackRow } from '@/components/track-row'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useAddToLane } from '@/hooks/use-add-to-lane'
import { useMe } from '@/lib/auth'
import { trackKey } from '@/lib/browse'
import { stagger } from '@/lib/motion'
import { queueQuery, useCurrentRoom } from '@/lib/room'
import { providersQuery } from '@/lib/services'
import { becauseLabel, suggestionsQuery, VIBE_LIMIT, type VibeScope } from '@/lib/suggestions'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

const empty: Record<VibeScope, string> = {
  mine: 'Queue a few songs and we’ll find more like them.',
  group: 'Once the room plays a few songs, we’ll find more like them.',
}

/**
 * "Keep the vibe going": songs like the room's, to add with a tap. Your vibe
 * is like your own songs; the group's is like everyone's. The list follows
 * the room from song to song, and shuffles on demand. With a source, only
 * that link's songs show.
 */
export function VibeSuggestions({ source, className }: { source?: { linkId: string; name: string }; className?: string }) {
  const me = useMe()
  const { room } = useCurrentRoom()
  const [scope, setScope] = useState<VibeScope>('mine')
  const [shuffle, setShuffle] = useState(0)
  const queue = useQuery({ ...queueQuery(room?.id ?? ''), enabled: !!room })
  const playing = queue.data?.items.find((i) => i.state === 'playing')?.id
  const list = useQuery({
    ...suggestionsQuery(room?.id ?? '', scope, playing, shuffle),
    enabled: !!room && queue.isSuccess,
    // Keep showing the last list while the next song's loads, but not
    // another vibe's.
    placeholderData: (prev, prevQuery) => (prevQuery?.queryKey[2] === scope ? prev : undefined),
  })
  const { add, status } = useAddToLane()
  const users = useQuery(usersQuery)
  const providers = useQuery(providersQuery)
  if (!room) return null

  const all = list.data?.items ?? []
  const items = source ? all.filter((s) => s.track.linkId === source.linkId) : all
  const nameOf = (id: string) => users.data?.find((u) => u.id === id)?.displayName
  // Tag songs with their service only when the list mixes services.
  const mixed = new Set(items.map((s) => s.track.linkId)).size > 1
  const iconOf = (provider: string) => providers.data?.find((p) => p.id === provider)?.icon ?? provider

  return (
    <section className={className} aria-labelledby="vibe-heading">
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 id="vibe-heading" className="text-headline">
          Keep the vibe going
        </h2>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Shuffle suggestions"
          disabled={list.isFetching}
          onClick={() => setShuffle((n) => n + 1)}
          className="-mr-1 text-muted-foreground"
        >
          <RefreshCw className={cn(list.isFetching && 'animate-spin')} />
        </Button>
      </div>
      <ToggleGroup
        type="single"
        value={scope}
        onValueChange={(v) => v && setScope(v as VibeScope)}
        aria-label="Whose vibe"
        className="glass mb-2"
      >
        <ToggleGroupItem value="mine">Your vibe</ToggleGroupItem>
        <ToggleGroupItem value="group">Group vibe</ToggleGroupItem>
      </ToggleGroup>
      {list.isPending ? (
        <SuggestionsSkeleton />
      ) : list.isError ? (
        <Notice>{errorMessage(list.error)}</Notice>
      ) : items.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">
          {source && all.length > 0 ? `None of these are on ${source.name}. Shuffle for more?` : empty[scope]}
        </p>
      ) : (
        <motion.ul
          key={`${scope}:${shuffle}:${playing ?? ''}`}
          variants={stagger}
          initial="hidden"
          animate="show"
          className={cn('flex flex-col transition-opacity duration-200', list.isPlaceholderData && 'opacity-60')}
        >
          {items.map((s) => (
            <TrackRow
              key={trackKey(s.track)}
              track={s.track}
              status={status(s.track)}
              onAdd={() => add([s.track])}
              providerIcon={mixed ? iconOf(s.track.provider) : undefined}
              note={becauseLabel(s.because, scope, me.id, nameOf)}
            />
          ))}
        </motion.ul>
      )}
    </section>
  )
}

function SuggestionsSkeleton() {
  return (
    <div className="flex flex-col gap-3" aria-hidden>
      {Array.from({ length: Math.min(VIBE_LIMIT, 4) }, (_, i) => (
        <div key={i} className="flex items-center gap-3 py-1.5">
          <Skeleton className="size-12 rounded-lg" />
          <div className="flex flex-1 flex-col gap-2">
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-3 w-1/3" />
          </div>
          <Skeleton className="size-10 rounded-full" />
        </div>
      ))}
    </div>
  )
}
