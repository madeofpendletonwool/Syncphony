import { useQuery } from '@tanstack/react-query'
import { ChevronDown, ExternalLink, UserX } from 'lucide-react'
import { motion } from 'motion/react'
import { useId, useState } from 'react'
import { errorMessage } from '@/api/errors'
import { Artwork } from '@/components/artwork'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { becauseText, kindText, releaseCoverUrl, releaseDateText, releaseTracksQuery, releaseYoutubeUrl, type DiscoveryRelease } from '@/lib/discover'
import { fadeUp } from '@/lib/motion'
import { cn } from '@/lib/utils'
import { SongRow } from './song-row'

/**
 * A recent release you don't have: what it is, when it came out and why
 * it's here. Open it for its songs, each with a preview, or search
 * YouTube for the whole thing.
 */
export function ReleaseCard({ release, isNew, onBlock }: { release: DiscoveryRelease; isNew?: boolean; onBlock: () => void }) {
  const [open, setOpen] = useState(false)
  const songsId = useId()
  const why = release.seed ? '' : becauseText(release)
  return (
    <motion.li variants={fadeUp} layout className="glass flex flex-col gap-3 rounded-3xl p-3">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="flex min-w-0 flex-1 items-center gap-3">
          <Artwork src={releaseCoverUrl(release, 250)} className="size-16 shrink-0 rounded-xl shadow-none" />
          <div className="min-w-0 flex-1">
            <p className="flex min-w-0 items-center gap-2">
              {isNew && <span className="shrink-0 rounded-full bg-primary px-2 py-0.5 text-caption font-medium text-primary-foreground">New</span>}
              <span className="truncate font-medium">{release.title}</span>
            </p>
            <p className="truncate text-sm text-muted-foreground">{release.artist}</p>
            <p className="truncate text-caption">
              <span className="text-muted-foreground">
                {kindText(release.kind)} · {releaseDateText(release.date)}
              </span>
              {why && <span className="text-primary"> · {why}</span>}
            </p>
          </div>
        </div>
        <div className="flex items-center justify-end gap-1">
          <Button variant="glass" size="sm" aria-expanded={open} aria-controls={songsId} onClick={() => setOpen((o) => !o)}>
            <ChevronDown data-icon="inline-start" className={cn('transition-transform', open && 'rotate-180')} />
            Songs
          </Button>
          <Button asChild variant="ghost" size="sm">
            <a href={releaseYoutubeUrl(release)} target="_blank" rel="noopener noreferrer" aria-label={`Open ${release.title} on YouTube`}>
              <ExternalLink data-icon="inline-start" />
              YouTube
            </a>
          </Button>
          <Button variant="ghost" size="icon-sm" aria-label={`${release.artist} isn't for me`} title={`${release.artist} isn't for me`} onClick={onBlock}>
            <UserX />
          </Button>
        </div>
      </div>
      {open && (
        <div id={songsId}>
          <Songs release={release} />
        </div>
      )}
    </motion.li>
  )
}

function Songs({ release }: { release: DiscoveryRelease }) {
  const songs = useQuery(releaseTracksQuery(release))
  if (songs.isPending) {
    return (
      <div className="flex flex-col gap-2">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-14 rounded-2xl" />
        ))}
      </div>
    )
  }
  if (songs.isError) return <Notice>{errorMessage(songs.error)}</Notice>
  if (songs.data.items.length === 0) {
    return <p className="px-1 text-sm text-muted-foreground">Deezer doesn&apos;t have its songs, so there are no previews. YouTube might.</p>
  }
  return (
    <ul className="-mx-1 flex flex-col" aria-label={`Songs on ${release.title}`}>
      {songs.data.items.map((it) => (
        <SongRow key={`${it.deezerId ?? ''}-${it.key}`} item={it} />
      ))}
    </ul>
  )
}
