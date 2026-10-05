import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { motion } from 'motion/react'
import { errorMessage } from '@/api/errors'
import { AlbumCard } from '@/components/album-card'
import { Artwork } from '@/components/artwork'
import { BackButton } from '@/components/back-button'
import { Notice } from '@/components/notice'
import { Skeleton } from '@/components/ui/skeleton'
import { artistQuery, artworkUrl } from '@/lib/browse'
import { easeOutExpo, stagger } from '@/lib/motion'

export const Route = createFileRoute('/_app/_authed/artist/$linkId/$artistId')({
  loader: ({ context, params }) => context.queryClient.prefetchQuery(artistQuery(params.linkId, params.artistId)),
  component: Artist,
})

function Artist() {
  const { linkId, artistId } = Route.useParams()
  const artist = useQuery(artistQuery(linkId, artistId))

  if (artist.isPending) {
    return (
      <>
        <BackButton />
        <div className="flex flex-col items-center gap-4 pt-4">
          <Skeleton className="size-40 rounded-full" />
          <Skeleton className="h-8 w-48" />
        </div>
      </>
    )
  }
  if (artist.isError) {
    return (
      <>
        <BackButton />
        <Notice className="mt-6">{errorMessage(artist.error)}</Notice>
      </>
    )
  }

  const { artist: a, albums } = artist.data
  // Newest first, like a discography.
  const sorted = [...albums].sort((x, y) => (y.year ?? 0) - (x.year ?? 0))

  return (
    <>
      <BackButton />
      <header className="flex flex-col items-center pt-2 pb-8 text-center">
        <motion.div
          initial={{ opacity: 0, scale: 0.9 }}
          animate={{ opacity: 1, scale: 1 }}
          transition={{ duration: 0.6, ease: easeOutExpo }}
        >
          <Artwork src={artworkUrl(linkId, a.artwork, 400)} className="size-40 rounded-full" />
        </motion.div>
        <h1 className="mt-5 text-display">{a.name}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          {albums.length} album{albums.length === 1 ? '' : 's'}
        </p>
      </header>

      <h2 className="mb-3 text-headline">Albums</h2>
      <motion.div variants={stagger} initial="hidden" animate="show" className="grid grid-cols-2 gap-x-4 gap-y-5 sm:grid-cols-3">
        {sorted.map((al) => (
          <AlbumCard
            key={al.id}
            album={al}
            linkId={linkId}
            subtitle={[al.year, al.trackCount && `${al.trackCount} songs`].filter(Boolean).join(' · ')}
          />
        ))}
      </motion.div>
    </>
  )
}
