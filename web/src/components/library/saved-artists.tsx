import { useQuery } from '@tanstack/react-query'
import { Compass, Search, Star } from 'lucide-react'
import { motion } from 'motion/react'
import { useState } from 'react'
import { AddToSetSheet } from '@/components/discover/add-to-set'
import { SaveArtistButton } from '@/components/library/save-artist'
import { ArtistLink } from '@/components/linked-names'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { imageUrl } from '@/lib/discover'
import { fadeUp, stagger } from '@/lib/motion'
import { roomsQuery } from '@/lib/room'
import { arrangeSaved, openableRef, savedArtistsQuery, type ArtistSort, type SavedArtist } from '@/lib/saved-artists'
import { usableLinksQuery } from '@/lib/services'
import { relativeTime } from '@/lib/time'
import { usersQuery } from '@/lib/users'

// The Library's Artists (MAD-841): everyone you saved, and where you heard them.

// With more than this many, a filter box shows.
const FILTER_FROM = 12

export function SavedArtists() {
  const saved = useQuery(savedArtistsQuery)
  const [sort, setSort] = useState<ArtistSort>('recent')
  const [filter, setFilter] = useState('')

  if (saved.isPending) {
    return (
      <div className="flex flex-col gap-3">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-16 rounded-2xl" />
        ))}
      </div>
    )
  }
  const all = saved.data ?? []
  if (all.length === 0) {
    return (
      <div className="glass flex flex-col gap-2 rounded-3xl p-5 text-sm text-muted-foreground">
        <p>
          No artists yet. When something good comes on, tap the <Star className="inline size-4 align-[-0.15em]" /> by the artist in now
          playing, or save them from their page.
        </p>
      </div>
    )
  }
  const shown = arrangeSaved(all, sort, filter)
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <ToggleGroup type="single" value={sort} onValueChange={(v) => v && setSort(v as ArtistSort)} aria-label="Sort artists">
          <ToggleGroupItem value="recent">Recent</ToggleGroupItem>
          <ToggleGroupItem value="name">A–Z</ToggleGroupItem>
        </ToggleGroup>
        {all.length > FILTER_FROM && (
          <label className="relative min-w-40 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter" aria-label="Filter artists" className="pl-9" />
          </label>
        )}
      </div>
      {shown.length === 0 ? (
        <p className="px-1 text-sm text-muted-foreground">No saved artist matches “{filter}”.</p>
      ) : (
        <motion.ul variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-1">
          {shown.map((a) => (
            <SavedArtistRow key={a.id} artist={a} />
          ))}
        </motion.ul>
      )}
    </div>
  )
}

function SavedArtistRow({ artist: a }: { artist: SavedArtist }) {
  const links = useQuery(usableLinksQuery)
  const rooms = useQuery(roomsQuery)
  const users = useQuery(usersQuery)
  const [adding, setAdding] = useState(false)
  const ref = openableRef(a, links.data)
  const room = a.roomId ? rooms.data?.find((r) => r.id === a.roomId) : undefined
  const by = a.queuedBy ? users.data?.find((u) => u.id === a.queuedBy) : undefined
  const heard = [room?.name, by && `queued by ${by.displayName.split(' ')[0]}`].filter(Boolean).join(' · ')

  return (
    <motion.li variants={fadeUp} className="flex items-center gap-2 rounded-2xl py-1.5 pr-1 pl-1 hover:bg-foreground/5">
      <ArtistLink
        name={a.name}
        id={ref?.id}
        linkId={ref?.linkId}
        className="flex min-w-0 flex-1 items-center gap-3 rounded-2xl no-underline hover:no-underline"
      >
        <ArtistPicture artist={a} />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium">{a.name}</span>
          <span className="block truncate text-caption text-muted-foreground">
            {heard ? `${heard} · ` : 'Saved '}
            {relativeTime(a.savedAt)}
          </span>
        </span>
      </ArtistLink>
      <button
        type="button"
        aria-label={`Add ${a.name} to a discovery set`}
        title="Add to a discovery set"
        onClick={() => setAdding(true)}
        className="grid size-9 shrink-0 place-items-center rounded-full text-muted-foreground transition-colors outline-none hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <Compass className="size-[1.1rem]" />
      </button>
      {/* Undo after removing brings it back from where it was last seen. */}
      <SaveArtistButton artists={[{ name: a.name, linkId: a.refs[0]?.linkId, artistId: a.refs[0]?.id }]} />
      <AddToSetSheet artist={a.name} open={adding} onOpenChange={setAdding} />
    </motion.li>
  )
}

function ArtistPicture({ artist: a }: { artist: SavedArtist }) {
  const [failed, setFailed] = useState(false)
  const src = imageUrl('artist', a.picture, 120)
  if (src && !failed) {
    return <img src={src} alt="" loading="lazy" onError={() => setFailed(true)} className="size-12 shrink-0 rounded-full object-cover" />
  }
  return (
    <span aria-hidden className="grid size-12 shrink-0 place-items-center rounded-full bg-muted text-sm font-semibold text-muted-foreground">
      {a.name.slice(0, 1).toUpperCase()}
    </span>
  )
}
