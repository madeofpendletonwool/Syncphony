import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Fragment } from 'react'
import type { Track } from '@/lib/now-playing'
import { usableLinksQuery } from '@/lib/services'
import { cn } from '@/lib/utils'

// Albums and artists open through the link the song was queued with. That
// works for your own links and shared ones; for anyone else's, it searches
// your services for the name instead.

function useOwnLink(track: Track) {
  const links = useQuery(usableLinksQuery)
  return track.linkId && links.data?.some((l) => l.id === track.linkId) ? track.linkId : undefined
}

const linkClass =
  'rounded-sm underline-offset-4 outline-none hover:text-foreground hover:underline focus-visible:ring-3 focus-visible:ring-ring/50'

/** The song's artists, each one a link to them. */
export function ArtistLinks({
  track,
  onNavigate,
  className,
}: {
  track: Track
  onNavigate?: () => void
  className?: string
}) {
  const linkId = useOwnLink(track)
  return (
    <span className={className}>
      {track.artists.map((name, i) => {
        const id = track.artistIds?.[i]
        return (
          <Fragment key={`${i}-${name}`}>
            {i > 0 && ', '}
            {linkId && id ? (
              <Link to="/artist/$linkId/$artistId" params={{ linkId, artistId: id }} onClick={onNavigate} className={linkClass}>
                {name}
              </Link>
            ) : (
              <Link to="/search" search={{ q: name, tab: 'artists' }} onClick={onNavigate} className={linkClass}>
                {name}
              </Link>
            )}
          </Fragment>
        )
      })}
    </span>
  )
}

/** The song's album, as a link to it. Nothing if the service didn't say. */
export function AlbumLink({
  track,
  onNavigate,
  className,
}: {
  track: Track
  onNavigate?: () => void
  className?: string
}) {
  const linkId = useOwnLink(track)
  if (!track.album) return null
  return linkId && track.albumId ? (
    <Link
      to="/album/$linkId/$albumId"
      params={{ linkId, albumId: track.albumId }}
      onClick={onNavigate}
      className={cn(linkClass, className)}
    >
      {track.album}
    </Link>
  ) : (
    <Link to="/search" search={{ q: track.album, tab: 'albums' }} onClick={onNavigate} className={cn(linkClass, className)}>
      {track.album}
    </Link>
  )
}
