import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { ChevronRight, Dices, History, LoaderCircle, Search as SearchIcon, Waypoints, X } from 'lucide-react'
import { motion } from 'motion/react'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { errorMessage } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { AlbumCard, ArtistCard, PlaylistCard } from '@/components/album-card'
import { Notice } from '@/components/notice'
import { PageHeader } from '@/components/page-header'
import { ProviderIcon } from '@/components/provider-icon'
import { Shelf } from '@/components/shelf'
import { JoinRoomPrompt } from '@/components/start-room'
import { TrackRow } from '@/components/track-row'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { VibeSuggestions } from '@/components/vibe-suggestions'
import { laneTrackOf, useAddToLane, type LaneTrack } from '@/hooks/use-add-to-lane'
import { useMe } from '@/lib/auth'
import {
  collectionQuery,
  interleave,
  playlistsQuery,
  randomTracks,
  searchQuery,
  similarQuery,
  trackKey,
  type AlbumResult,
  type ArtistResult,
  type LinkCollection,
  type SearchGroup,
} from '@/lib/browse'
import { myHistoryQuery } from '@/lib/history'
import { fadeUp, stagger } from '@/lib/motion'
import { playbackQuery } from '@/lib/playback'
import { clearSearches, forgetSearch, rememberSearch, useRecentSearches } from '@/lib/recent-searches'
import { useCurrentRoom } from '@/lib/room'
import { providersQuery, sourceName, usableLinksQuery } from '@/lib/services'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

type ServiceLink = components['schemas']['ServiceLink']

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

  const links = useQuery(usableLinksQuery)
  const results = useQuery({ ...searchQuery(q), enabled: q !== '', placeholderData: keepPreviousData })

  // A search is worth remembering once it found something and the typing
  // has settled.
  const found = results.data?.query === q && results.data.groups.some((g) => g.tracks.length + g.albums.length + g.artists.length > 0)
  useEffect(() => {
    if (!found) return
    const t = setTimeout(() => rememberSearch(q), 1500)
    return () => clearTimeout(t)
  }, [found, q])

  const searchFor = (next: string) => {
    setText(next)
    void navigate({ search: (s) => ({ ...s, q: next, tab: undefined }), replace: true })
  }

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
            if (text.trim()) rememberSearch(text)
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
      {links.data && <Sources links={links.data} />}

      {links.data && links.data.length === 0 ? (
        <NoLinks />
      ) : !q ? (
        links.data && <Browse links={links.data} onSearch={searchFor} />
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
  const me = useMe()
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
          {g.ownerId !== me.id ? (
            'is shared with you but isn\u2019t available right now.'
          ) : g.error?.code === 'needs_relink' ? (
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

/**
 * Before searching: quick picks. Your recent searches, a surprise, songs
 * like what's playing, like the room's vibe, and ones you queued lately, then from every link
 * its playlists (Liked Songs and starred songs among them), albums played
 * lately and most, new additions, and favorites, each kind grouped across
 * links.
 */
function Browse({ links, onSearch }: { links: ServiceLink[]; onSearch: (q: string) => void }) {
  const providers = useQuery(providersQuery)
  const recent = useRecentSearches()
  const can = (l: ServiceLink, cap: 'playlists' | 'collection' | 'recommendations') =>
    providers.data?.find((p) => p.id === l.provider)?.capabilities[cap] ?? false
  const withPlaylists = links.filter((l) => can(l, 'playlists'))
  const withCollection = links.filter((l) => can(l, 'collection'))
  const recommends = links.some((l) => can(l, 'recommendations'))
  // Name the source only when shelves of a kind come from more than one.
  const named = withCollection.length > 1
  return (
    <div className="mt-6 flex flex-col gap-8">
      {recent.length > 0 && <RecentSearches searches={recent} onSearch={onSearch} />}
      {recommends && <SurpriseMe />}
      {recommends && <LikeWhatsPlaying />}
      <VibeSuggestions />
      <RecentAdds links={links} />
      {withPlaylists.map((l) => (
        <PlaylistShelf key={l.id} link={l} />
      ))}
      {shelves.map((shelf) =>
        withCollection.map((l) => <CollectionShelf key={`${shelf.key}:${l.id}`} link={l} shelf={shelf} named={named} />),
      )}
      {withPlaylists.length + withCollection.length === 0 && !recommends && recent.length === 0 && (
        <p className="mt-10 text-center text-sm text-muted-foreground">Search every service you&apos;ve linked, all at once.</p>
      )}
    </div>
  )
}

function RecentSearches({ searches, onSearch }: { searches: string[]; onSearch: (q: string) => void }) {
  return (
    <Section title="Recent searches" action={{ label: 'Clear', onClick: clearSearches }}>
      <ul className="flex flex-wrap gap-2">
        {searches.map((q) => (
          <li key={q} className="glass flex items-center rounded-full text-sm">
            <button
              type="button"
              onClick={() => onSearch(q)}
              className="flex items-center gap-1.5 rounded-full py-1.5 pl-3 outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <History className="size-3.5 text-muted-foreground" />
              {q}
            </button>
            <button
              type="button"
              aria-label={`Forget “${q}”`}
              onClick={() => forgetSearch(q)}
              className="grid size-7 place-items-center rounded-full text-muted-foreground outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <X className="size-3.5" />
            </button>
          </li>
        ))}
      </ul>
    </Section>
  )
}

/** How many songs a surprise adds. */
const SURPRISE_COUNT = 5

/** Adds a few songs picked at random to your lane. */
function SurpriseMe() {
  const { add } = useAddToLane()
  const [busy, setBusy] = useState(false)
  const surprise = async () => {
    setBusy(true)
    try {
      const { tracks } = await randomTracks(SURPRISE_COUNT)
      if (tracks.length === 0) toast({ message: 'Couldn’t find anything to pick from right now.', tone: 'error' })
      else add(tracks)
    } catch (err) {
      toast({ message: errorMessage(err), tone: 'error' })
    } finally {
      setBusy(false)
    }
  }
  return (
    <motion.button
      variants={fadeUp}
      initial="hidden"
      animate="show"
      type="button"
      disabled={busy}
      onClick={() => void surprise()}
      className="glass group flex items-center gap-4 rounded-3xl p-4 text-left outline-none transition-transform active:scale-[0.99] focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-70"
    >
      <span className="grid size-12 shrink-0 place-items-center rounded-2xl bg-primary/15 text-primary">
        {busy ? <LoaderCircle className="size-6 animate-spin" /> : <Dices className="size-6 transition-transform group-hover:rotate-12" />}
      </span>
      <span className="min-w-0">
        <span className="block font-medium">Surprise me</span>
        <span className="block text-sm text-muted-foreground">Add {SURPRISE_COUNT} random songs to your lane</span>
      </span>
    </motion.button>
  )
}

/** Songs like the one the room is playing, while it plays. */
function LikeWhatsPlaying() {
  const { room } = useCurrentRoom()
  const playback = useQuery({ ...playbackQuery(room?.id ?? ''), enabled: !!room })
  const item = playback.data?.item
  const similar = useQuery({ ...similarQuery(room?.id ?? '', item?.id ?? ''), enabled: !!room && !!item })
  const { add, status } = useAddToLane()
  if (!room || !item || similar.isError || similar.data?.tracks.length === 0) return null
  return (
    <Section title={`More like “${item.track.title}”`}>
      {similar.data ? (
        <motion.ul variants={stagger} initial="hidden" animate="show" className="flex flex-col">
          {similar.data.tracks.slice(0, 5).map((t) => (
            <TrackRow key={trackKey(t)} track={t} status={status(t)} onAdd={() => add([t])} />
          ))}
        </motion.ul>
      ) : (
        <RowsSkeleton rows={3} />
      )}
    </Section>
  )
}

/** Songs you queued lately in this room, to queue again. */
function RecentAdds({ links }: { links: ServiceLink[] }) {
  const me = useMe()
  const { room } = useCurrentRoom()
  const history = useQuery({ ...myHistoryQuery(room?.id ?? '', me.id), enabled: !!room })
  const { add, status } = useAddToLane()
  if (!room || !history.data) return null
  const usable = new Set(links.filter((l) => l.status === 'ok').map((l) => l.id))
  const seen = new Set<string>()
  const tracks: LaneTrack[] = []
  for (const { item } of history.data) {
    const linkId = item.track.linkId
    if (!linkId || item.autopilot || !(usable.has(linkId) || room.matching.borrow)) continue
    const t = laneTrackOf(item, linkId)
    if (seen.has(trackKey(t))) continue
    seen.add(trackKey(t))
    tracks.push(t)
    if (tracks.length === 5) break
  }
  if (tracks.length === 0) return null
  return (
    <Section title="Your recent adds">
      <motion.ul variants={stagger} initial="hidden" animate="show" className="flex flex-col">
        {tracks.map((t) => (
          <TrackRow key={trackKey(t)} track={t} status={status(t)} onAdd={() => add([t])} />
        ))}
      </motion.ul>
    </Section>
  )
}

type ShelfSpec = { key: keyof Omit<LinkCollection, 'linkId' | 'provider'>; title: string; artists?: boolean }

const shelves: ShelfSpec[] = [
  { key: 'recentlyPlayed', title: 'Played lately' },
  { key: 'mostPlayed', title: 'On repeat' },
  { key: 'recentlyAdded', title: 'Recently added' },
  { key: 'savedAlbums', title: 'Favorite albums' },
  { key: 'savedArtists', title: 'Favorite artists', artists: true },
]

/** One of a link's collection lists, or nothing if it's empty. */
function CollectionShelf({ link, shelf, named }: { link: ServiceLink; shelf: ShelfSpec; named: boolean }) {
  const collection = useQuery(collectionQuery(link.id))
  const source = useSourceName(link)
  const title = named ? `${shelf.title} · ${source}` : shelf.title

  // A failing service already shows on its playlists shelf, or will when searched.
  if (collection.isError) return null
  if (!collection.data) {
    return (
      <Section title={title}>
        <ShelfSkeleton round={shelf.artists} />
      </Section>
    )
  }
  const items = collection.data[shelf.key]
  if (items.length === 0) return null
  return (
    <Section title={title}>
      <Shelf>
        {shelf.artists
          ? (items as ArtistResult[]).map((a) => (
              <div key={a.id} className="w-28 shrink-0 snap-start">
                <ArtistCard artist={a} linkId={link.id} />
              </div>
            ))
          : (items as AlbumResult[]).map((a) => (
              <AlbumCard key={a.id} album={a} linkId={link.id} className="w-36 shrink-0 snap-start" />
            ))}
      </Shelf>
    </Section>
  )
}

/** What to call a link: "Navidrome", or "Sam's Navidrome" when it's shared. */
function useSourceName(link: ServiceLink) {
  const me = useMe()
  const providers = useQuery(providersQuery)
  const users = useQuery(usersQuery)
  const p = providers.data?.find((p) => p.id === link.provider)
  const owner = users.data?.find((u) => u.id === link.ownerId)
  return sourceName(p?.name ?? link.provider, owner?.displayName, link.ownerId === me.id)
}

function ShelfSkeleton({ round }: { round?: boolean }) {
  return (
    <div className="flex gap-4 overflow-hidden">
      {Array.from({ length: round ? 4 : 3 }, (_, i) => (
        <Skeleton key={i} className={cn('shrink-0', round ? 'size-28 rounded-full' : 'size-36 rounded-2xl')} />
      ))}
    </div>
  )
}

function PlaylistShelf({ link }: { link: ServiceLink }) {
  const playlists = useQuery(playlistsQuery(link.id))
  const title = `${useSourceName(link)} playlists`

  if (playlists.isError) {
    return (
      <Section title={title}>
        <Notice>{errorMessage(playlists.error)}</Notice>
      </Section>
    )
  }
  if (playlists.data && playlists.data.playlists.length === 0) return null
  return (
    <Section title={title}>
      {playlists.data ? (
        <Shelf>
          {playlists.data.playlists.map((pl) => (
            <PlaylistCard key={pl.id} playlist={pl} linkId={link.id} className="w-36 shrink-0 snap-start" />
          ))}
        </Shelf>
      ) : (
        <ShelfSkeleton />
      )}
    </Section>
  )
}

function Section({
  title,
  more,
  action,
  children,
}: {
  title: string
  more?: () => void
  action?: { label: string; onClick: () => void }
  children: ReactNode
}) {
  return (
    <section>
      <div className="mb-2 flex items-center justify-between gap-2">
        <h2 className="min-w-0 truncate text-headline">{title}</h2>
        {more && (
          <Button variant="ghost" size="sm" onClick={more} className="-mr-2 text-muted-foreground">
            See all
            <ChevronRight data-icon="inline-end" />
          </Button>
        )}
        {action && (
          <Button variant="ghost" size="sm" onClick={action.onClick} className="-mr-2 text-muted-foreground">
            {action.label}
          </Button>
        )}
      </div>
      {children}
    </section>
  )
}

function ResultsSkeleton() {
  return <RowsSkeleton rows={6} className="mt-6" />
}

function RowsSkeleton({ rows, className }: { rows: number; className?: string }) {
  return (
    <div className={cn('flex flex-col gap-3', className)}>
      {Array.from({ length: rows }, (_, i) => (
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

/** Which libraries a search covers, when some are shared by others. */
function Sources({ links }: { links: ServiceLink[] }) {
  const me = useMe()
  const providers = useQuery(providersQuery)
  const users = useQuery(usersQuery)
  if (!links.some((l) => l.ownerId !== me.id)) return null
  return (
    <p className="mt-3 flex flex-wrap items-center gap-1.5 px-1 text-caption text-muted-foreground">
      Searching
      {links.map((l) => {
        const p = providers.data?.find((p) => p.id === l.provider)
        const owner = users.data?.find((u) => u.id === l.ownerId)
        return (
          <span key={l.id} className="glass inline-flex items-center gap-1 rounded-full py-0.5 pr-2 pl-1">
            <ProviderIcon icon={p?.icon ?? l.provider} className="size-4 rounded-full [&_svg]:size-2.5" />
            {sourceName(p?.name ?? l.provider, owner?.displayName, l.ownerId === me.id)}
          </span>
        )
      })}
    </p>
  )
}

function NoLinks() {
  const me = useMe()
  if (me.guest) {
    return (
      <motion.section variants={fadeUp} initial="hidden" animate="show" className="glass mt-6 flex flex-col items-center gap-3 rounded-3xl px-6 py-12 text-center">
        <div className="grid size-14 place-items-center rounded-2xl bg-muted text-muted-foreground">
          <Waypoints className="size-7" />
        </div>
        <h2 className="text-headline">Nothing to search yet</h2>
        <p className="text-sm text-muted-foreground">Guests search the music the room shares. Ask the host to share a service.</p>
      </motion.section>
    )
  }
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
        <p className="mt-1 text-sm text-muted-foreground">Search goes through your own music accounts, and ones others share with you.</p>
      </motion.div>
      <motion.div variants={fadeUp}>
        <Button asChild>
          <Link to="/settings/services">Link a service</Link>
        </Button>
      </motion.div>
    </motion.section>
  )
}
