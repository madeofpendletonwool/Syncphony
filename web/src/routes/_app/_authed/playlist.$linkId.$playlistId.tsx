import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { ListPlus, LoaderCircle } from 'lucide-react'
import { motion } from 'motion/react'
import { useEffect } from 'react'
import { errorMessage } from '@/api/errors'
import { Artwork } from '@/components/artwork'
import { BackButton } from '@/components/back-button'
import { Notice } from '@/components/notice'
import { ProviderIcon } from '@/components/provider-icon'
import { TrackRow } from '@/components/track-row'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useAddToLane } from '@/hooks/use-add-to-lane'
import { artworkUrl, playlistsQuery, playlistTracksQuery, totalDuration, trackKey } from '@/lib/browse'
import { easeOutExpo, fadeUp, stagger } from '@/lib/motion'
import { providersQuery } from '@/lib/services'

export const Route = createFileRoute('/_app/_authed/playlist/$linkId/$playlistId')({
  loader: ({ context, params }) => {
    void context.queryClient.prefetchQuery(playlistsQuery(params.linkId))
    return context.queryClient.prefetchInfiniteQuery(playlistTracksQuery(params.linkId, params.playlistId))
  },
  component: Playlist,
})

// The server queues at most 100 songs per request.
const MAX_ADD = 100

function Playlist() {
  const { linkId, playlistId } = Route.useParams()
  const playlists = useQuery(playlistsQuery(linkId))
  const pages = useInfiniteQuery(playlistTracksQuery(linkId, playlistId))
  const providers = useQuery(providersQuery)
  const { add, status } = useAddToLane()

  // Load the whole playlist, a page at a time, so it can all be queued.
  const { hasNextPage, isFetchingNextPage, isError, fetchNextPage } = pages
  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage && !isError) void fetchNextPage()
  }, [hasNextPage, isFetchingNextPage, isError, fetchNextPage])

  if (pages.isPending) {
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
  if (pages.isError && !pages.data) {
    return (
      <>
        <BackButton />
        <Notice className="mt-6">{errorMessage(pages.error)}</Notice>
      </>
    )
  }

  const tracks = pages.data.pages.flatMap((p) => p.tracks)
  const info = playlists.data?.playlists.find((p) => p.id === playlistId)
  const provider = pages.data.pages[0]?.provider
  const p = providers.data?.find((p) => p.id === provider)
  const loading = hasNextPage || isFetchingNextPage
  // A song can be in a playlist twice; it's queued once.
  const seen = new Set<string>()
  const waiting = tracks.filter((t) => {
    const k = trackKey(t)
    if (seen.has(k) || status(t) !== 'idle') return false
    seen.add(k)
    return true
  })
  const meta = [`${tracks.length}${loading ? '+' : ''} song${tracks.length === 1 ? '' : 's'}`, !loading && totalDuration(tracks)].filter(Boolean)

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
          <Artwork
            src={artworkUrl(linkId, info?.artwork ?? tracks[0]?.artwork, 600)}
            className="size-56 rounded-3xl sm:size-64"
          />
        </motion.div>
        <motion.h1 variants={fadeUp} className="mt-6 text-title">
          {info?.name ?? 'Playlist'}
        </motion.h1>
        <motion.p variants={fadeUp} className="mt-1 flex items-center gap-1.5 text-caption text-muted-foreground">
          {p && <ProviderIcon icon={p.icon} className="size-4 rounded-[0.3rem] [&_svg]:size-2.5" />}
          {meta.join(' · ')}
          {loading && <LoaderCircle className="size-3 animate-spin" />}
        </motion.p>
        <motion.div variants={fadeUp} className="mt-5">
          <Button size="lg" disabled={waiting.length === 0} onClick={() => add(waiting.slice(0, MAX_ADD))}>
            <ListPlus data-icon="inline-start" />
            {waiting.length === 0
              ? tracks.length === 0
                ? 'Nothing to add'
                : 'All in your lane'
              : waiting.length > MAX_ADD
                ? `Add the next ${MAX_ADD} to my lane`
                : waiting.length === tracks.length
                  ? 'Add playlist to my lane'
                  : `Add ${waiting.length} more to my lane`}
          </Button>
        </motion.div>
      </motion.header>

      {pages.isError && <Notice className="mb-4">{errorMessage(pages.error)}</Notice>}
      {tracks.length === 0 && !loading ? (
        <p className="mt-6 text-center text-sm text-muted-foreground">No songs here that can be queued.</p>
      ) : (
        <motion.ul variants={stagger} initial="hidden" animate="show" className="glass flex flex-col rounded-3xl px-3 py-2">
          {tracks.map((t, i) => (
            <TrackRow key={`${trackKey(t)}:${i}`} track={t} status={status(t)} onAdd={() => add([t])} number={i + 1} />
          ))}
        </motion.ul>
      )}
    </>
  )
}
