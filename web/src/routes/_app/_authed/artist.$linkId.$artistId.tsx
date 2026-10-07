import { useMutation, useQueries, useQuery } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { ListPlus, Shuffle } from 'lucide-react'
import { motion } from 'motion/react'
import { useMemo } from 'react'
import { errorMessage } from '@/api/errors'
import { AlbumCard } from '@/components/album-card'
import {
  AddAllButton,
  Bio,
  GenreChips,
  Members,
  PageSection,
  RelatedShelf,
  SongList,
  SongsSkeleton,
} from '@/components/artist-parts'
import { Artwork } from '@/components/artwork'
import { BackButton } from '@/components/back-button'
import { Notice } from '@/components/notice'
import { ProviderIcon } from '@/components/provider-icon'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { laneTrackOf, useAddToLane, type LaneTrack } from '@/hooks/use-add-to-lane'
import {
  artistAboutQuery,
  artistElsewhereQuery,
  artistRelatedQuery,
  artistShuffle,
  artistTracksQuery,
  discography,
  kindLabel,
  kindOf,
  mergeAlbums,
  roomArtistPlaysQuery,
  type LinkedAlbum,
} from '@/lib/artist'
import { artistQuery, artworkUrl } from '@/lib/browse'
import { easeOutExpo, fadeUp, stagger } from '@/lib/motion'
import { providersQuery, usableLinksQuery } from '@/lib/services'
import { toast } from '@/lib/toast'

export const Route = createFileRoute('/_app/_authed/artist/$linkId/$artistId')({
  loader: ({ context, params }) => context.queryClient.prefetchQuery(artistQuery(params.linkId, params.artistId)),
  component: Artist,
})

// How many top songs "Add top 5" adds, and how many songs a shuffle does.
const TOP_ADD = 5
const SHUFFLE = 10

function Artist() {
  const { linkId, artistId } = Route.useParams()
  const artist = useQuery(artistQuery(linkId, artistId))
  const about = useQuery(artistAboutQuery(linkId, artistId))
  const tracks = useQuery(artistTracksQuery(linkId, artistId))
  const related = useQuery(artistRelatedQuery(linkId, artistId))
  const elsewhere = useQuery(artistElsewhereQuery(linkId, artistId))
  const providers = useQuery(providersQuery)
  const { add, status } = useAddToLane()

  // The same artist's albums on your other services.
  const others = useQueries({
    queries: (elsewhere.data ?? []).map((e) => artistQuery(e.linkId, e.artist.id)),
  })
  const iconOf = (provider: string) => providers.data?.find((p) => p.id === provider)?.icon

  const shuffle = useMutation({
    mutationFn: () => artistShuffle(linkId, artistId, SHUFFLE),
    onSuccess: (ts) => (ts.length > 0 ? add(ts) : toast({ message: 'No songs to shuffle' })),
    onError: (err) => toast({ message: errorMessage(err), tone: 'error' }),
  })

  const merged = useMemo(() => {
    if (!artist.data) return []
    const own: LinkedAlbum[] = artist.data.albums.map((a) => ({ ...a, linkId }))
    const more = others.flatMap((q) => (q.data ? [q.data.albums.map((a) => ({ ...a, linkId: q.data.linkId }))] : []))
    return mergeAlbums(own, more)
  }, [artist.data, others, linkId])

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
  const kinds = about.data?.albumKinds
  const shelves = discography(merged, kinds)
  const top = tracks.data?.top ?? []
  const topWaiting = top.filter((t) => status(t) === 'idle').slice(0, TOP_ADD)
  const n = about.data
  const subtitle = [n?.about, `${albums.length} release${albums.length === 1 ? '' : 's'}`].filter(Boolean).join(' · ')

  return (
    <>
      <BackButton />
      <motion.header variants={stagger} initial="hidden" animate="show" className="flex flex-col items-center pt-2 text-center">
        <motion.div
          initial={{ opacity: 0, scale: 0.9 }}
          animate={{ opacity: 1, scale: 1 }}
          transition={{ duration: 0.6, ease: easeOutExpo }}
        >
          <Artwork src={artworkUrl(linkId, a.artwork, 400)} className="size-40 rounded-full" />
        </motion.div>
        <motion.h1 variants={fadeUp} className="mt-5 text-display">
          {a.name}
        </motion.h1>
        <motion.p variants={fadeUp} className="mt-1 text-sm text-muted-foreground">
          {subtitle}
        </motion.p>
        {n && n.facts.length > 0 && (
          <motion.p variants={fadeUp} className="mt-1 text-caption text-muted-foreground">
            {n.facts.map((f) => f.text).join(' · ')}
          </motion.p>
        )}
        {n && <GenreChips genres={n.genres} linkId={linkId} className="mt-4" />}
        <motion.div variants={fadeUp} className="mt-5 flex flex-wrap justify-center gap-2">
          <Button size="lg" disabled={topWaiting.length === 0} onClick={() => add(topWaiting)}>
            <ListPlus data-icon="inline-start" />
            {top.length > 0 && topWaiting.length === 0 ? 'Top songs in your lane' : `Add top ${TOP_ADD}`}
          </Button>
          <Button size="lg" variant="glass" disabled={shuffle.isPending || albums.length === 0} onClick={() => shuffle.mutate()}>
            <Shuffle data-icon="inline-start" />
            Shuffle
          </Button>
        </motion.div>
        {elsewhere.data && elsewhere.data.length > 0 && (
          <motion.div variants={fadeUp} className="mt-4 flex flex-wrap justify-center gap-2">
            {elsewhere.data.map((e) => (
              <Link
                key={e.linkId}
                to="/artist/$linkId/$artistId"
                params={{ linkId: e.linkId, artistId: e.artist.id }}
                className="flex items-center gap-1.5 rounded-full bg-muted px-3 py-1 text-caption text-muted-foreground hover:bg-accent hover:text-foreground"
              >
                {iconOf(e.provider) && <ProviderIcon icon={iconOf(e.provider)!} className="size-4 rounded-[0.3rem] [&_svg]:size-2.5" />}
                Also on {e.accountLabel}
              </Link>
            ))}
          </motion.div>
        )}
      </motion.header>

      {n?.bio && (
        <div className="mt-8">
          <Bio text={n.bio} url={n.bioUrl} />
        </div>
      )}

      <TopSongs tracks={top} pending={tracks.isPending} />
      <PopularHere name={a.name} />

      {shelves.map((s) => (
        <PageSection key={s.title} title={s.title}>
          <motion.div variants={stagger} initial="hidden" animate="show" className="grid grid-cols-2 gap-x-4 gap-y-5 sm:grid-cols-3">
            {s.albums.map((al) => {
              const kind = kindOf(al, kinds)
              const subtitle = [al.year, kind === 'album' ? al.trackCount && `${al.trackCount} songs` : kindLabel(kind)]
              const p = al.linkId === linkId ? undefined : others.find((q) => q.data?.linkId === al.linkId)?.data?.provider
              return (
                <AlbumCard
                  key={`${al.linkId}:${al.id}`}
                  album={al}
                  linkId={al.linkId}
                  providerIcon={p && iconOf(p)}
                  subtitle={subtitle.filter(Boolean).join(' · ')}
                />
              )
            })}
          </motion.div>
        </PageSection>
      ))}

      {tracks.data && tracks.data.appearsOn.length > 0 && (
        <PageSection title="Appears on">
          <SongList tracks={tracks.data.appearsOn} />
        </PageSection>
      )}

      {related.isPending ? (
        <PageSection title="Similar songs">
          <SongsSkeleton rows={3} />
        </PageSection>
      ) : (
        related.data && (
          <>
            {related.data.tracks.length > 0 && (
              <PageSection title="Similar songs" action={<AddAllButton tracks={related.data.tracks} label="Add all" />}>
                <SongList tracks={related.data.tracks} />
              </PageSection>
            )}
            {related.data.artists.length > 0 && (
              <PageSection title="Fans also like">
                <RelatedShelf artists={related.data.artists} linkId={linkId} />
              </PageSection>
            )}
          </>
        )
      )}

      {n && n.members.length > 0 && (
        <PageSection title="Members">
          <Members members={n.members} />
        </PageSection>
      )}
    </>
  )
}

function TopSongs({ tracks, pending }: { tracks: LaneTrack[]; pending: boolean }) {
  if (pending) {
    return (
      <PageSection title="Top songs">
        <SongsSkeleton />
      </PageSection>
    )
  }
  if (tracks.length === 0) return null
  return (
    <PageSection title="Top songs">
      <SongList tracks={tracks} numbered />
    </PageSection>
  )
}

/** The artist's songs the current room played most, to add again. */
function PopularHere({ name }: { name: string }) {
  const { room } = useAddToLane()
  const plays = useQuery({ ...roomArtistPlaysQuery(room?.id ?? '', name), enabled: !!room })
  const links = useQuery(usableLinksQuery)
  if (!room || !plays.data) return null
  const usable = new Set(links.data?.filter((l) => l.status === 'ok').map((l) => l.id))
  const counts = new Map<string, number>()
  const tracks: LaneTrack[] = []
  for (const { item, plays: n } of plays.data) {
    const linkId = item.track.linkId
    if (!linkId || !(usable.has(linkId) || room.matching.borrow)) continue
    const t = laneTrackOf(item, linkId)
    counts.set(t.trackId, n)
    tracks.push(t)
  }
  if (tracks.length === 0) return null
  return (
    <PageSection title={`Popular in ${room.name}`}>
      <SongList tracks={tracks} note={(t) => `${counts.get(t.trackId)} play${counts.get(t.trackId) === 1 ? '' : 's'} here`} />
    </PageSection>
  )
}
