import { useQuery } from '@tanstack/react-query'
import { AddButton } from '@/components/add-button'
import { useAddToLane } from '@/hooks/use-add-to-lane'
import type { TrackResult } from '@/lib/browse'
import type { QueueItem } from '@/lib/playback'
import { usableLinksQuery } from '@/lib/services'

/**
 * Puts a song from history back in your lane. Only shown when you can play
 * it: it came from one of your links, or one shared with you.
 */
export function RequeueButton({ item }: { item: QueueItem }) {
  const links = useQuery(usableLinksQuery)
  const { add, status } = useAddToLane()
  const linkId = item.track.linkId
  if (!linkId || !links.data?.some((l) => l.id === linkId && l.status === 'ok')) return null
  const t = item.track
  const track: TrackResult = {
    linkId,
    provider: t.provider,
    trackId: t.trackId,
    title: t.title,
    artists: t.artists.map((name, i) => ({ name, id: t.artistIds?.[i] || undefined })),
    album: t.album ? { title: t.album, id: t.albumId } : undefined,
    durationMs: t.durationMs,
    explicit: t.explicit,
    artwork: t.artwork,
  }
  return <AddButton status={status(track)} onAdd={() => add([track])} title={item.track.title} className="size-9" />
}
