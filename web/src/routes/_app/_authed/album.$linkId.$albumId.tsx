import { useQuery } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { ListPlus } from 'lucide-react'
import { motion } from 'motion/react'
import { errorMessage } from '@/api/errors'
import { AlbumCard } from '@/components/album-card'
import { Bio, GenreChips, PageSection } from '@/components/artist-parts'
import { Artwork } from '@/components/artwork'
import { BackButton } from '@/components/back-button'
import { Notice } from '@/components/notice'
import { ProviderIcon } from '@/components/provider-icon'
import { TrackRow } from '@/components/track-row'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useAddToLane } from '@/hooks/use-add-to-lane'
import { albumAboutQuery, kindLabel, type AlbumAbout } from '@/lib/artist'
import { albumQuery, artistQuery, artworkUrl, totalDuration } from '@/lib/browse'
import { Shelf } from '@/components/shelf'
import { easeOutExpo, fadeUp, stagger } from '@/lib/motion'
import { providersQuery } from '@/lib/services'

export const Route = createFileRoute('/_app/_authed/album/$linkId/$albumId')({
  loader: ({ context, params }) => context.queryClient.prefetchQuery(albumQuery(params.linkId, params.albumId)),
  component: Album,
})

// The server queues at most 100 songs per request.
const MAX_ADD = 100

function Album() {
  const { linkId, albumId } = Route.useParams()
  const album = useQuery(albumQuery(linkId, albumId))
  const about = useQuery(albumAboutQuery(linkId, albumId))
  const providers = useQuery(providersQuery)
  const { add, status } = useAddToLane()

  if (album.isPending) {
    return (
      <>
        <BackButton />
        <div className="flex flex-col items-center gap-4 pt-4">
          <Skeleton className="size-56 rounded-3xl" />
          <Skeleton className="h-7 w-48" />
          <Skeleton className="h-4 w-32" />
        </div>
      </>
    )
  }
  if (album.isError) {
    return (
      <>
        <BackButton />
        <Notice className="mt-6">{errorMessage(album.error)}</Notice>
      </>
    )
  }

  const { album: a, tracks, provider } = album.data
  const p = providers.data?.find((p) => p.id === provider)
  const waiting = tracks.filter((t) => status(t) === 'idle')
  const kind = a.kind ?? about.data?.kind
  const meta = [
    kind && kind !== 'album' && kindLabel(kind),
    a.year,
    `${tracks.length} song${tracks.length === 1 ? '' : 's'}`,
    totalDuration(tracks),
  ].filter(Boolean)
  const firstArtist = a.artists.find((ar) => ar.id)

  return (
    <>
      <BackButton />
      <motion.header
        variants={stagger}
        initial="hidden"
        animate="show"
        className="flex flex-col items-center pt-2 pb-6 text-center"
      >
        <motion.div
          initial={{ opacity: 0, scale: 0.92 }}
          animate={{ opacity: 1, scale: 1 }}
          transition={{ duration: 0.6, ease: easeOutExpo }}
        >
          <Artwork src={artworkUrl(linkId, a.artwork, 600)} className="size-56 rounded-3xl sm:size-64" />
        </motion.div>
        <motion.h1 variants={fadeUp} className="mt-6 text-title">
          {a.title}
        </motion.h1>
        <motion.p variants={fadeUp} className="mt-1 text-muted-foreground">
          {a.artists.map((ar, i) => (
            <span key={ar.id ?? ar.name}>
              {i > 0 && ', '}
              {ar.id ? (
                <Link
                  to="/artist/$linkId/$artistId"
                  params={{ linkId, artistId: ar.id }}
                  className="font-medium text-foreground hover:underline"
                >
                  {ar.name}
                </Link>
              ) : (
                ar.name
              )}
            </span>
          ))}
        </motion.p>
        <motion.p variants={fadeUp} className="mt-1 flex items-center gap-1.5 text-caption text-muted-foreground">
          {p && <ProviderIcon icon={p.icon} className="size-4 rounded-[0.3rem] [&_svg]:size-2.5" />}
          {meta.join(' · ')}
        </motion.p>
        {about.data && <ReleaseLine about={about.data} year={a.year} />}
        {about.data && <GenreChips genres={about.data.genres} linkId={linkId} className="mt-3" />}
        <motion.div variants={fadeUp} className="mt-5">
          <Button
            size="lg"
            disabled={waiting.length === 0}
            onClick={() => add(waiting.slice(0, MAX_ADD))}
          >
            <ListPlus data-icon="inline-start" />
            {waiting.length === 0
              ? 'All in your lane'
              : waiting.length === tracks.length
                ? 'Add album to my lane'
                : `Add ${waiting.length} more to my lane`}
          </Button>
        </motion.div>
      </motion.header>

      <motion.ul variants={stagger} initial="hidden" animate="show" className="glass flex flex-col rounded-3xl px-3 py-2">
        {tracks.map((t, i) => (
          <TrackRow key={t.trackId} track={t} status={status(t)} onAdd={() => add([t])} number={i + 1} hideAlbum />
        ))}
      </motion.ul>

      {about.data?.about && (
        <PageSection title="About this album">
          <Bio text={about.data.about} url={about.data.aboutUrl} />
        </PageSection>
      )}
      {firstArtist?.id && <MoreBy linkId={linkId} artistId={firstArtist.id} name={firstArtist.name} albumId={albumId} />}
    </>
  )
}

/** When it first came out, if that's not its year here, and on which labels. */
function ReleaseLine({ about, year }: { about: AlbumAbout; year?: number }) {
  const first = about.firstReleased?.slice(0, 4)
  const parts = [
    first && Number(first) !== year && `First released ${first}`,
    about.labels.length > 0 && about.labels.join(', '),
  ].filter(Boolean)
  if (parts.length === 0) return null
  return (
    <motion.p variants={fadeUp} className="mt-1 text-caption text-muted-foreground">
      {parts.join(' · ')}
    </motion.p>
  )
}

/** The artist's other albums, newest first. */
function MoreBy({ linkId, artistId, name, albumId }: { linkId: string; artistId: string; name: string; albumId: string }) {
  const artist = useQuery(artistQuery(linkId, artistId))
  const albums = (artist.data?.albums ?? []).filter((a) => a.id !== albumId).sort((x, y) => (y.year ?? 0) - (x.year ?? 0))
  if (albums.length === 0) return null
  return (
    <PageSection
      title={`More by ${name}`}
      action={
        <Link to="/artist/$linkId/$artistId" params={{ linkId, artistId }} className="text-sm text-muted-foreground hover:text-foreground">
          See artist
        </Link>
      }
    >
      <Shelf>
        {albums.map((a) => (
          <AlbumCard key={a.id} album={a} linkId={linkId} subtitle={a.year ? String(a.year) : undefined} className="w-36 shrink-0 snap-start" />
        ))}
      </Shelf>
    </PageSection>
  )
}
