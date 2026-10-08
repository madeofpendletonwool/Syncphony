import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Tag } from 'lucide-react'
import { motion } from 'motion/react'
import { ApiError, errorMessage } from '@/api/errors'
import { AddAllButton, PageSection, RelatedShelf, SongList, SongsSkeleton } from '@/components/artist-parts'
import { BackButton } from '@/components/back-button'
import { Notice } from '@/components/notice'
import { Skeleton } from '@/components/ui/skeleton'
import { genreQuery } from '@/lib/artist'
import { fadeUp, stagger } from '@/lib/motion'

export const Route = createFileRoute('/_app/_authed/genre/$linkId')({
  validateSearch: (search: Record<string, unknown>): { name: string } => ({
    name: typeof search.name === 'string' ? search.name.trim() : '',
  }),
  component: Genre,
})

/** A genre's artists, those on the service first, and songs by them. */
function Genre() {
  const { linkId } = Route.useParams()
  const { name } = Route.useSearch()
  const genre = useQuery({ ...genreQuery(linkId, name), enabled: !!name })

  return (
    <>
      <BackButton />
      <motion.header variants={stagger} initial="hidden" animate="show" className="flex flex-col items-center pt-2 pb-2 text-center">
        <motion.span variants={fadeUp} className="grid size-20 place-items-center rounded-3xl bg-primary/15 text-primary">
          <Tag className="size-9" />
        </motion.span>
        <motion.h1 variants={fadeUp} className="mt-5 text-display capitalize">
          {name || 'Genre'}
        </motion.h1>
        <motion.p variants={fadeUp} className="mt-1 text-sm text-muted-foreground">
          Genre
        </motion.p>
      </motion.header>

      {genre.isPending && name ? (
        <>
          <PageSection title="Artists">
            <div className="flex gap-4">
              {Array.from({ length: 4 }, (_, i) => (
                <Skeleton key={i} className="size-28 shrink-0 rounded-full" />
              ))}
            </div>
          </PageSection>
          <PageSection title="Songs">
            <SongsSkeleton />
          </PageSection>
        </>
      ) : !name || (genre.error instanceof ApiError && genre.error.status === 404) ? (
        <Notice tone="info" className="mt-8">
          Nothing is known about this genre yet.
        </Notice>
      ) : genre.isError ? (
        <Notice className="mt-8">{errorMessage(genre.error)}</Notice>
      ) : (
        genre.data && (
          <>
            {genre.data.artists.length > 0 && (
              <PageSection title="Artists">
                <RelatedShelf artists={genre.data.artists} linkId={linkId} />
              </PageSection>
            )}
            {genre.data.tracks.length > 0 ? (
              <PageSection title="Songs" action={<AddAllButton tracks={genre.data.tracks} label="Add all" />}>
                <SongList tracks={genre.data.tracks} />
              </PageSection>
            ) : (
              <Notice tone="info" className="mt-8">
                None of its artists are on this service.
              </Notice>
            )}
          </>
        )
      )}
    </>
  )
}
