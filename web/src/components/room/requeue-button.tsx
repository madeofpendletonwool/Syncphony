import { useQuery } from '@tanstack/react-query'
import { AddButton } from '@/components/add-button'
import { useAddToLane, type LaneTrack } from '@/hooks/use-add-to-lane'
import type { QueueItem } from '@/lib/playback'
import { usableLinksQuery } from '@/lib/services'

/**
 * Puts a song the room played back in your lane. Only shown when you can:
 * it came from a link you can use (yours, or shared), or the room lets
 * people borrow songs from each other's services.
 */
export function RequeueButton({ item }: { item: QueueItem }) {
  const links = useQuery(usableLinksQuery)
  const { add, status, room } = useAddToLane()
  const linkId = item.track.linkId
  const usable = links.data?.some((l) => l.id === linkId && l.status === 'ok')
  if (!linkId || !(usable || room?.matching.borrow)) return null
  const t = item.track
  const track: LaneTrack = {
    fromItemId: item.id,
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
