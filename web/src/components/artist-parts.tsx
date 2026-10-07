import { Link } from '@tanstack/react-router'
import { ExternalLink, Search, Users } from 'lucide-react'
import { motion } from 'motion/react'
import { useState, type ReactNode } from 'react'
import { ArtistCard } from '@/components/album-card'
import { Shelf } from '@/components/shelf'
import { TrackRow } from '@/components/track-row'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useAddToLane } from '@/hooks/use-add-to-lane'
import { memberYears, rolesLine, type ArtistMember, type RelatedArtist } from '@/lib/artist'
import type { TrackResult } from '@/lib/browse'
import { fadeUp, stagger } from '@/lib/motion'
import { cn } from '@/lib/utils'

/** A page section: a heading, with an optional action beside it. */
export function PageSection({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="mt-10">
      <div className="mb-3 flex items-center justify-between gap-2">
        <h2 className="min-w-0 truncate text-headline">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  )
}

/** Genres as chips that open their pages. */
export function GenreChips({ genres, linkId, className }: { genres: string[]; linkId: string; className?: string }) {
  if (genres.length === 0) return null
  return (
    <div className={cn('flex flex-wrap justify-center gap-1.5', className)}>
      {genres.map((g) => (
        <Link
          key={g}
          to="/genre/$linkId"
          params={{ linkId }}
          search={{ name: g }}
          className="rounded-full bg-muted px-3 py-1 text-caption font-medium capitalize text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          {g}
        </Link>
      ))}
    </div>
  )
}

/** A Wikipedia summary that opens up when tapped, with its source. */
export function Bio({ text, url, label = 'Read more on Wikipedia' }: { text: string; url?: string; label?: string }) {
  const [open, setOpen] = useState(false)
  return (
    <motion.div variants={fadeUp} className="glass rounded-3xl px-5 py-4">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className={cn('text-left leading-relaxed text-pretty', !open && 'line-clamp-4')}
      >
        {text}
      </button>
      {url && (
        <a
          href={url}
          target="_blank"
          rel="noreferrer"
          className="mt-2 inline-flex items-center gap-1 text-caption text-muted-foreground hover:text-foreground"
        >
          {label}
          <ExternalLink className="size-3" />
        </a>
      )}
    </motion.div>
  )
}

/** A band's members, current first. */
export function Members({ members }: { members: ArtistMember[] }) {
  return (
    <motion.ul variants={stagger} initial="hidden" animate="show" className="grid grid-cols-1 gap-2 sm:grid-cols-2">
      {members.map((m) => {
        const line = [rolesLine(m.roles), memberYears(m)].filter(Boolean).join(' · ')
        return (
          <motion.li key={m.name} variants={fadeUp} className="flex items-center gap-3 rounded-2xl bg-muted/50 px-3 py-2">
            <span className="grid size-9 shrink-0 place-items-center rounded-full bg-muted text-muted-foreground">
              <Users className="size-4" />
            </span>
            <div className="min-w-0">
              <p className={cn('truncate font-medium', !m.current && 'text-muted-foreground')}>{m.name}</p>
              {line && <p className="truncate text-caption text-muted-foreground">{line}</p>}
            </div>
          </motion.li>
        )
      })}
    </motion.ul>
  )
}

/**
 * Related artists in a row: those on the link open their page, the rest
 * search for them everywhere.
 */
export function RelatedShelf({ artists, linkId }: { artists: RelatedArtist[]; linkId: string }) {
  return (
    <Shelf>
      {artists.map((r) => (
        <div key={r.name} className="w-28 shrink-0 snap-start">
          {r.artist ? (
            <ArtistCard artist={r.artist} linkId={linkId} />
          ) : (
            <motion.div variants={fadeUp}>
              <Link
                to="/search"
                search={{ q: r.name, tab: 'artists' }}
                className="group flex flex-col items-center rounded-2xl text-center outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <span className="grid aspect-square w-full place-items-center rounded-full bg-muted text-2xl font-semibold text-muted-foreground transition-transform duration-300 ease-out-expo group-hover:scale-[1.03]">
                  {r.name.charAt(0).toUpperCase()}
                </span>
                <p className="mt-2 w-full truncate text-sm font-medium">{r.name}</p>
                <p className="flex items-center gap-1 text-caption text-muted-foreground">
                  <Search className="size-3" />
                  Search
                </p>
              </Link>
            </motion.div>
          )}
        </div>
      ))}
    </Shelf>
  )
}

/** Songs with one-tap adds, and an add-them-all action when there are a few. */
export function SongList({
  tracks,
  numbered,
  hideAlbum,
  note,
}: {
  tracks: TrackResult[]
  numbered?: boolean
  hideAlbum?: boolean
  note?: (t: TrackResult) => string | undefined
}) {
  const { add, status } = useAddToLane()
  return (
    <motion.ul variants={stagger} initial="hidden" animate="show" className="glass flex flex-col rounded-3xl px-3 py-2">
      {tracks.map((t, i) => (
        <TrackRow
          key={`${t.linkId}:${t.trackId}`}
          track={t}
          status={status(t)}
          onAdd={() => add([t])}
          number={numbered ? i + 1 : undefined}
          hideAlbum={hideAlbum}
          note={note?.(t)}
        />
      ))}
    </motion.ul>
  )
}

/** Adds the songs not already in your lane. */
export function AddAllButton({ tracks, label }: { tracks: TrackResult[]; label: string }) {
  const { add, status } = useAddToLane()
  const waiting = tracks.filter((t) => status(t) === 'idle')
  return (
    <Button variant="ghost" size="sm" disabled={waiting.length === 0} onClick={() => add(waiting)} className="-mr-2 text-muted-foreground">
      {waiting.length === 0 ? 'All in your lane' : label}
    </Button>
  )
}

/** Rows of placeholder songs. */
export function SongsSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="glass flex flex-col gap-3 rounded-3xl px-3 py-3" aria-busy>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-3">
          <Skeleton className="size-12 rounded-lg" />
          <div className="flex flex-1 flex-col gap-1.5">
            <Skeleton className="h-4 w-1/2 rounded-md" />
            <Skeleton className="h-3 w-1/3 rounded-md" />
          </div>
        </div>
      ))}
    </div>
  )
}
