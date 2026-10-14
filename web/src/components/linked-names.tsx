import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Fragment, useState, type MouseEvent } from 'react'
import type { Track } from '@/lib/now-playing'
import {
  albumTarget,
  artistTarget,
  directLink,
  resolveAlbumQuery,
  resolveArtistQuery,
  trackCredits,
  type Credit,
  type NameTarget,
} from '@/lib/names'
import { usableLinksQuery } from '@/lib/services'
import { cn } from '@/lib/utils'

// Artist and album names open their pages (MAD-837). Through the link they
// came from when you can use it; otherwise the name is looked up on your
// services when tapped, and a search for it if none has it.

const linkClass =
  'rounded-sm underline-offset-4 outline-none hover:text-foreground hover:underline focus-visible:ring-3 focus-visible:ring-ring/50'

function useDirect(linkId: string | undefined, id: string | undefined) {
  const links = useQuery({ ...usableLinksQuery, enabled: !!linkId && !!id })
  return directLink(linkId, id, links.data)
}

// Taps on a name shouldn't also reach the row or card around it.
const stop = (e: MouseEvent) => e.stopPropagation()

/** A name that's looked up when tapped. Its href is the search, for opening in a new tab. */
function LookupLink({
  children,
  search,
  target,
  className,
}: {
  children: string
  search: { q: string; tab: 'artists' | 'albums' }
  target: () => Promise<NameTarget>
  className?: string
}) {
  const navigate = useNavigate()
  const [busy, setBusy] = useState(false)
  return (
    <Link
      to="/search"
      search={search}
      aria-busy={busy || undefined}
      onClick={(e) => {
        e.stopPropagation()
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
        e.preventDefault()
        if (busy) return
        setBusy(true)
        void target()
          .then((t) => navigate(t))
          .finally(() => setBusy(false))
      }}
      className={cn(linkClass, busy && 'animate-pulse', className)}
    >
      {children}
    </Link>
  )
}

/** One artist's name, as a link to them. */
export function ArtistLink({ name, id, linkId, className }: Credit & { linkId?: string; className?: string }) {
  const qc = useQueryClient()
  const direct = useDirect(linkId, id)
  if (direct && id) {
    return (
      <Link to="/artist/$linkId/$artistId" params={{ linkId: direct, artistId: id }} onClick={stop} className={cn(linkClass, className)}>
        {name}
      </Link>
    )
  }
  return (
    <LookupLink
      search={{ q: name, tab: 'artists' }}
      target={() => artistTarget(name, () => qc.fetchQuery(resolveArtistQuery(name)))}
      className={className}
    >
      {name}
    </LookupLink>
  )
}

type ArtistsProps = ({ track: Pick<Track, 'artists' | 'artistIds' | 'linkId'> } | { artists: Credit[]; linkId?: string }) & {
  className?: string
}

/** Every credited artist, comma separated, each a link to them. */
export function ArtistLinks(props: ArtistsProps) {
  const [artists, linkId] = 'track' in props ? [trackCredits(props.track), props.track.linkId] : [props.artists, props.linkId]
  return (
    <span className={props.className}>
      {artists.map((a, i) => (
        <Fragment key={`${i}-${a.name}`}>
          {i > 0 && ', '}
          <ArtistLink name={a.name} id={a.id} linkId={linkId} />
        </Fragment>
      ))}
    </span>
  )
}

type AlbumProps = (
  | { track: Pick<Track, 'album' | 'albumId' | 'artists' | 'linkId'> }
  | { title: string; id?: string; artist?: string; linkId?: string }
) & { className?: string }

/** An album's title, as a link to it. Nothing for a song whose service didn't name its album. */
export function AlbumLink(props: AlbumProps) {
  const { title, id, artist, linkId } =
    'track' in props
      ? { title: props.track.album, id: props.track.albumId, artist: props.track.artists[0], linkId: props.track.linkId }
      : props
  const qc = useQueryClient()
  const direct = useDirect(linkId, id)
  if (!title) return null
  if (direct && id) {
    return (
      <Link to="/album/$linkId/$albumId" params={{ linkId: direct, albumId: id }} onClick={stop} className={cn(linkClass, props.className)}>
        {title}
      </Link>
    )
  }
  return (
    <LookupLink
      search={{ q: title, tab: 'albums' }}
      target={() => albumTarget(title, () => qc.fetchQuery(resolveAlbumQuery(title, artist)))}
      className={props.className}
    >
      {title}
    </LookupLink>
  )
}
