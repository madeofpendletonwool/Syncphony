import { useQuery } from '@tanstack/react-query'
import { BookOpen, ExternalLink, Sparkles } from 'lucide-react'
import { motion } from 'motion/react'
import { Skeleton } from '@/components/ui/skeleton'
import { linerNotesQuery, releaseLine, type LinerNotesFact } from '@/lib/liner-notes'
import { fadeUp, stagger } from '@/lib/motion'
import type { NowPlaying } from '@/lib/now-playing'
import { cn } from '@/lib/utils'

const factIcon: Record<LinerNotesFact['kind'], string> = {
  cover: '🎙️',
  live: '🎤',
  samples: '🎛️',
  sampled_by: '🔁',
  origin: '📍',
  first_released: '📅',
}

/**
 * The now-playing song's liner notes: where it came out, who made it, a
 * few facts, and a bit about the artist.
 */
export function LinerNotesPanel({ np, className }: { np: NowPlaying; className?: string }) {
  const { roomId, itemId } = np
  const notes = useQuery({ ...linerNotesQuery(roomId ?? '', itemId ?? ''), enabled: !!roomId && !!itemId })

  if (notes.isPending && roomId && itemId) {
    return (
      <div className={cn('flex flex-col gap-3 py-2', className)} aria-busy>
        <Skeleton className="h-5 w-2/3 rounded-lg" />
        <Skeleton className="h-16 rounded-2xl" />
        <Skeleton className="h-4 w-1/2 rounded-lg" />
        <Skeleton className="h-4 w-3/5 rounded-lg" />
        <p className="text-caption text-muted-foreground">Looking it up on MusicBrainz…</p>
      </div>
    )
  }
  const n = notes.data
  if (!n) {
    return (
      <div className={cn('flex flex-col items-center gap-2 py-10 text-center', className)}>
        <span className="grid size-12 place-items-center rounded-2xl bg-(--pal-text)/12 text-(--pal-text)">
          <BookOpen className="size-6" />
        </span>
        <p className="text-headline">{notes.isError ? "Couldn't load liner notes" : 'No liner notes'}</p>
        <p className="text-sm text-muted-foreground">
          {notes.isError ? 'Try again in a moment.' : "MusicBrainz doesn't know this one yet."}
        </p>
      </div>
    )
  }

  const release = releaseLine(n)
  return (
    <motion.div variants={stagger} initial="hidden" animate="show" className={cn('flex flex-col gap-5', className)}>
      {release && (
        <motion.p variants={fadeUp} className="text-sm font-medium text-muted-foreground">
          {release}
        </motion.p>
      )}

      {n.facts.length > 0 && (
        <motion.ul variants={fadeUp} className="flex flex-col gap-2">
          {n.facts.map((f) => (
            <li key={f.kind + f.text} className="flex items-start gap-3 rounded-2xl bg-(--pal-text)/10 px-3.5 py-3 text-sm">
              <span aria-hidden className="text-base leading-5">
                {factIcon[f.kind] ?? '✨'}
              </span>
              <span>{f.text}</span>
            </li>
          ))}
        </motion.ul>
      )}

      {n.credits.length > 0 && (
        <motion.dl variants={fadeUp} className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          {n.credits.map((c) => (
            <div key={c.role} className="contents">
              <dt className="text-muted-foreground">{c.role}</dt>
              <dd className="font-medium">{c.names.join(', ')}</dd>
            </div>
          ))}
        </motion.dl>
      )}

      {n.artist && (n.artist.bio || n.artist.about) && (
        <motion.section variants={fadeUp} className="flex flex-col gap-1.5">
          <h3 className="flex items-center gap-1.5 text-headline">
            <Sparkles className="size-4 text-(--pal-text)" />
            {n.artist.name}
          </h3>
          {n.artist.about && <p className="text-caption text-muted-foreground">{n.artist.about}</p>}
          {n.artist.bio && <p className="text-sm/relaxed text-foreground/90">{n.artist.bio}</p>}
          {n.artist.bioUrl && (
            <a
              href={n.artist.bioUrl}
              target="_blank"
              rel="noreferrer"
              className="inline-flex w-fit items-center gap-1 rounded-sm text-caption text-muted-foreground underline-offset-4 outline-none hover:text-foreground hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              From Wikipedia
              <ExternalLink className="size-3" />
            </a>
          )}
        </motion.section>
      )}

      <motion.a
        variants={fadeUp}
        href={`https://musicbrainz.org/recording/${encodeURIComponent(n.recordingMbid)}`}
        target="_blank"
        rel="noreferrer"
        className="inline-flex w-fit items-center gap-1 rounded-sm text-caption text-muted-foreground underline-offset-4 outline-none hover:text-foreground hover:underline focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        Credits from MusicBrainz
        <ExternalLink className="size-3" />
      </motion.a>
    </motion.div>
  )
}
