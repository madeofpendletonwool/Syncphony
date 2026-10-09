import { ListMusic } from 'lucide-react'
import { Artwork } from '@/components/artwork'
import { songArtworkUrl, type PlaylistSong } from '@/lib/playlists'
import { cn } from '@/lib/utils'

/**
 * A playlist's cover: its first song's artwork, or a grid of its first
 * four once it has that many different ones.
 */
export function PlaylistCover({ playlistId, songs, size = 300, className }: { playlistId: string; songs: PlaylistSong[]; size?: number; className?: string }) {
  const seen = new Set<string>()
  const covers = songs.filter((s) => {
    const k = s.track.album ?? s.track.artwork ?? s.id
    if (!s.track.artwork || seen.has(k)) return false
    seen.add(k)
    return true
  })
  if (covers.length === 0) {
    return (
      <div className={cn('grid aspect-square shrink-0 place-items-center rounded-xl bg-primary/12 text-primary shadow-float', className)}>
        <ListMusic className="size-1/3" />
      </div>
    )
  }
  if (covers.length < 4) return <Artwork src={songArtworkUrl(playlistId, covers[0], size)} className={className} />
  return (
    <div className={cn('grid aspect-square shrink-0 grid-cols-2 overflow-hidden rounded-xl bg-muted shadow-float', className)}>
      {covers.slice(0, 4).map((s) => (
        <Artwork key={s.id} src={songArtworkUrl(playlistId, s, Math.round(size / 2))} className="size-full rounded-none shadow-none outline-0" />
      ))}
    </div>
  )
}
