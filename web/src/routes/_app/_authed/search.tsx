import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { ChevronRight, LoaderCircle, Search as SearchIcon, Waypoints, X } from 'lucide-react'
import { motion } from 'motion/react'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { errorMessage } from '@/api/errors'
import { AlbumCard, ArtistCard } from '@/components/album-card'
import { Notice } from '@/components/notice'
import { PageHeader } from '@/components/page-header'
import { JoinRoomPrompt } from '@/components/start-room'
import { TrackRow } from '@/components/track-row'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { useAddToLane } from '@/hooks/use-add-to-lane'
import { interleave, searchQuery, trackKey, type SearchGroup } from '@/lib/browse'
import { fadeUp, stagger } from '@/lib/motion'
import { linksQuery, providersQuery } from '@/lib/services'
import { cn } from '@/lib/utils'

const tabs = ['all', 'songs', 'albums', 'artists'] as const
type Tab = (typeof tabs)[number]

export const Route = createFileRoute('/_app/_authed/search')({
  validateSearch: (search: Record<string, unknown>): { q?: string; tab?: Tab } => ({
    q: typeof search.q === 'string' && search.q.trim() ? search.q : undefined,
    tab: tabs.includes(search.tab as Tab) && search.tab !== 'all' ? (search.tab as Tab) : undefined,
  }),
  component: Search,
})

function Search() {
  const { q = '', tab = 'all' } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const [text, setText] = useState(q)
  const inputRef = useRef<HTMLInputElement>(null)

  // The URL holds the query, so going back from an album lands on the same
  // results. Typing updates it after a short pause.
  useEffect(() => {
    const next = text.trim()
    if (next === q) return
    const t = setTimeout(() => void navigate({ search: (s) => ({ ...s, q: next || undefined }), replace: true }), 250)
    return () => clearTimeout(t)
  }, [text, q, navigate])

  const links = useQuery(linksQuery)
  const results = useQuery({ ...searchQuery(q), enabled: q !== '', placeholderData: keepPreviousData })

  return (
    <>
      <PageHeader title="Search" />
      <div className="sticky top-[calc(env(safe-area-inset-top)+0.75rem)] z-10 flex flex-col gap-2">
        <form
          role="search"
          className="relative"
          onSubmit={(e) => {
            e.preventDefault()
            inputRef.current?.blur()
            void navigate({ search: (s) => ({ ...s, q: text.trim() || undefined }), replace: true })
          }}
        >
          <SearchIcon className="pointer-events-none absolute top-1/2 left-4 z-10 size-5 -translate-y-1/2 text-muted-foreground" />
          <Input
            ref={inputRef}
            type="search"
            inputMode="search"
            enterKeyHint="search"
            autoFocus={!q}
            aria-label="Search songs, albums and artists"
            placeholder="Songs, albums, artists"
            value={text}
            onChange={(e) => setText(e.target.value)}
            className="glass h-13 rounded-2xl pr-12 pl-12 text-base [&::-webkit-search-cancel-button]:hidden"
          />
          {results.isFetching ? (
            <LoaderCircle className="absolute top-1/2 right-4 z-10 size-5 -translate-y-1/2 animate-spin text-muted-foreground" />
          ) : (
            text && (
              <button
                type="button"
                aria-label="Clear search"
                onClick={() => {
                  setText('')
                  inputRef.current?.focus()
                }}
                className="absolute top-1/2 right-2.5 z-10 grid size-8 -translate-y-1/2 place-items-center rounded-full text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <X className="size-4" />
              </button>
            )
          )}
        </form>
        {q && results.data && (
          <ToggleGroup
            type="single"
            value={tab}
            onValueChange={(v) => v && navigate({ search: (s) => ({ ...s, tab: v === 'all' ? undefined : (v as Tab) }), replace: true })}
            aria-label="Show"
            className="glass self-start"
          >
            <ToggleGroupItem value="all">All</ToggleGroupItem>
            <ToggleGroupItem value="songs">Songs</ToggleGroupItem>
            <ToggleGroupItem value="albums">Albums</ToggleGroupItem>
            <ToggleGroupItem value="artists">Artists</ToggleGroupItem>
          </ToggleGroup>
        )}
      </div>

      <JoinRoomPrompt className="mt-4" />

      {links.data && links.data.length === 0 ? (
        <NoLinks />
      ) : !q ? (
        <p className="mt-16 text-center text-sm text-muted-foreground">
          Search every service you&apos;ve linked, all at once.
        </p>
      ) : results.isPending ? (
        <ResultsSkeleton />
      ) : results.isError ? (
        <Notice className="mt-6">{errorMessage(results.error)}</Notice>
      ) : (
        <div className={cn('transition-opacity duration-200', results.isPlaceholderData && 'opacity-60')}>
          <Results key={results.data.query} groups={results.data.groups} tab={tab} />
        </div>
      )}
    </>
  )
}

function Results({ groups, tab }: { groups: SearchGroup[]; tab: Tab }) {
  const navigate = useNavigate({ from: Route.fullPath })
  const { add, status } = useAddToLane()
  const providers = useQuery(providersQuery)
  const iconOf = (provider: string) => providers.data?.find((p) => p.id === provider)?.icon ?? provider
  const nameOf = (provider: string) => providers.data?.find((p) => p.id === provider)?.name ?? provider

  // Tag results with their service only when they come from more than one.
  const ok = groups.filter((g) => !g.error)
  const mixed = ok.length > 1
  const tag = (linkId: string) => {
    if (!mixed) return undefined
    const g = groups.find((g) => g.linkId === linkId)
    return g && iconOf(g.provider)
  }

  const tracks = interleave(ok, (g) => g.tracks)
  const albums = interleave(ok, (g) => g.albums)
  const artists = interleave(ok, (g) => g.artists)
  const failed = groups.filter((g) => g.error)
  const empty = tracks.length + albums.length + artists.length === 0
  const seeAll = (t: Tab) => () => void navigate({ search: (s) => ({ ...s, tab: t }), replace: true })

  const songList = (limit?: number) => (
    <motion.ul variants={stagger} initial="hidden" animate="show" className="flex flex-col">
      {tracks.slice(0, limit).map((t) => (
        <TrackRow key={trackKey(t)} track={t} status={status(t)} onAdd={() => add([t])} providerIcon={tag(t.linkId)} />
      ))}
    </motion.ul>
  )
  const albumGrid = (
    <motion.div variants={stagger} initial="hidden" animate="show" className="grid grid-cols-2 gap-x-4 gap-y-5 sm:grid-cols-3">
      {albums.map((a) => (
        <AlbumCard key={`${a.linkId}:${a.id}`} album={a} linkId={a.linkId} providerIcon={tag(a.linkId)} />
      ))}
    </motion.div>
  )
  const artistGrid = (
    <motion.div variants={stagger} initial="hidden" animate="show" className="grid grid-cols-3 gap-x-4 gap-y-5 sm:grid-cols-4">
      {artists.map((a) => (
        <ArtistCard key={`${a.linkId}:${a.id}`} artist={a} linkId={a.linkId} providerIcon={tag(a.linkId)} />
      ))}
    </motion.div>
  )

  return (
    <div className="mt-6 flex flex-col gap-8">
      {failed.map((g) => (
        <Notice key={g.linkId}>
          <span className="font-medium">{nameOf(g.provider)}</span> ({g.accountLabel}){' '}
          {g.error?.code === 'needs_relink' ? (
            <>
              needs linking again.{' '}
              <Link to="/settings/services" className="font-medium underline underline-offset-2">
                Re-link
              </Link>
            </>
          ) : (
            "couldn't be searched right now."
          )}
        </Notice>
      ))}

      {empty ? (
        failed.length < groups.length && (
          <p className="mt-10 text-center text-sm text-muted-foreground">Nothing matches that. Try fewer words?</p>
        )
      ) : tab === 'songs' ? (
        songList()
      ) : tab === 'albums' ? (
        albumGrid
      ) : tab === 'artists' ? (
        artistGrid
      ) : (
        <>
          {tracks.length > 0 && (
            <Section title="Songs" more={tracks.length > 5 ? seeAll('songs') : undefined}>
              {songList(5)}
            </Section>
          )}
          {albums.length > 0 && (
            <Section title="Albums" more={albums.length > 4 ? seeAll('albums') : undefined}>
              <Shelf>
                {albums.slice(0, 10).map((a) => (
                  <AlbumCard
                    key={`${a.linkId}:${a.id}`}
                    album={a}
                    linkId={a.linkId}
                    providerIcon={tag(a.linkId)}
                    className="w-36 shrink-0 snap-start"
                  />
                ))}
              </Shelf>
            </Section>
          )}
          {artists.length > 0 && (
            <Section title="Artists" more={artists.length > 4 ? seeAll('artists') : undefined}>
              <Shelf>
                {artists.slice(0, 10).map((a) => (
                  <div key={`${a.linkId}:${a.id}`} className="w-28 shrink-0 snap-start">
                    <ArtistCard artist={a} linkId={a.linkId} providerIcon={tag(a.linkId)} />
                  </div>
                ))}
              </Shelf>
            </Section>
          )}
        </>
      )}
    </div>
  )
}

function Section({ title, more, children }: { title: string; more?: () => void; children: ReactNode }) {
  return (
    <section>
      <div className="mb-2 flex items-center justify-between">
        <h2 className="text-headline">{title}</h2>
        {more && (
          <Button variant="ghost" size="sm" onClick={more} className="-mr-2 text-muted-foreground">
            See all
            <ChevronRight data-icon="inline-end" />
          </Button>
        )}
      </div>
      {children}
    </section>
  )
}

/** A sideways-scrolling row that bleeds to the screen edges. */
function Shelf({ children }: { children: ReactNode }) {
  return (
    <motion.div
      variants={stagger}
      initial="hidden"
      animate="show"
      className="-mx-gutter flex snap-x snap-mandatory scroll-px-gutter gap-4 overflow-x-auto px-gutter pb-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
    >
      {children}
    </motion.div>
  )
}

function ResultsSkeleton() {
  return (
    <div className="mt-6 flex flex-col gap-3">
      {Array.from({ length: 6 }, (_, i) => (
        <div key={i} className="flex items-center gap-3 py-1.5">
          <Skeleton className="size-12 rounded-lg" />
          <div className="flex flex-1 flex-col gap-2">
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-3 w-1/3" />
          </div>
          <Skeleton className="size-10 rounded-full" />
        </div>
      ))}
    </div>
  )
}

function NoLinks() {
  return (
    <motion.section
      variants={stagger}
      initial="hidden"
      animate="show"
      className="glass mt-6 flex flex-col items-center gap-4 rounded-3xl px-6 py-12 text-center"
    >
      <motion.div variants={fadeUp} className="grid size-14 place-items-center rounded-2xl bg-primary/15 text-primary">
        <Waypoints className="size-7" />
      </motion.div>
      <motion.div variants={fadeUp}>
        <h2 className="text-headline">Link a service to search</h2>
        <p className="mt-1 text-sm text-muted-foreground">Search goes through your own music accounts.</p>
      </motion.div>
      <motion.div variants={fadeUp}>
        <Button asChild>
          <Link to="/settings/services">Link a service</Link>
        </Button>
      </motion.div>
    </motion.section>
  )
}
