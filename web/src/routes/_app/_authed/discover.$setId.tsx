import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Check, LoaderCircle, Pencil, RefreshCw, Star, Trash2, Users, X } from 'lucide-react'
import { motion } from 'motion/react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { errorMessage } from '@/api/errors'
import { BackButton } from '@/components/back-button'
import { ConfirmButton } from '@/components/confirm-button'
import { ArtistSearch } from '@/components/discover/artist-search'
import { FeedCard } from '@/components/discover/feed-card'
import { RoomSheet, StarredSheet } from '@/components/discover/import-sheets'
import { ReleaseCard } from '@/components/discover/release-card'
import { SetTabs } from '@/components/discover/set-tabs'
import { Suggestions } from '@/components/discover/suggestions'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Slider } from '@/components/ui/slider'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  addArtist,
  block,
  blocksQuery,
  deleteSet,
  feedItems,
  feedQuery,
  freshQuery,
  freshWindows,
  mixFresh,
  refreshFeed,
  removeArtist,
  setChanged,
  setQuery,
  setsQuery,
  sourceText,
  stopPreview,
  suggestionsQuery,
  thumb,
  unblock,
  updateSet,
  wantChanged,
  wantRelease,
  type DiscoveryFeed,
  type DiscoveryFresh,
  type DiscoveryFreshDays,
  type DiscoveryItem,
  type DiscoveryMode,
  type DiscoveryRelease,
  type DiscoveryRoomWeight,
  type DiscoverySet,
} from '@/lib/discover'
import { roomsQuery } from '@/lib/room'
import { tap } from '@/lib/haptics'
import { stagger } from '@/lib/motion'
import { toast } from '@/lib/toast'

export const Route = createFileRoute('/_app/_authed/discover/$setId')({
  loader: ({ context, params }) => context.queryClient.prefetchQuery(setQuery(params.setId)),
  component: SetPage,
})

function SetPage() {
  const { setId } = Route.useParams()
  const set = useQuery(setQuery(setId))
  // A preview stops when you leave.
  useEffect(() => stopPreview, [])

  if (set.isPending) {
    return (
      <>
        <BackButton fallback="/discover" />
        <Skeleton className="mt-4 h-9 w-56" />
        <Skeleton className="mt-6 h-24 rounded-3xl" />
      </>
    )
  }
  if (set.isError) {
    return (
      <>
        <BackButton fallback="/discover" />
        <Notice className="mt-6">{errorMessage(set.error)}</Notice>
      </>
    )
  }
  return (
    <>
      <BackButton fallback="/discover" />
      <Header set={set.data} />
      <Seeds set={set.data} />
      <Tuning set={set.data} />
      <Suggestions set={set.data} />
      {set.data.artists.length === 0 && !set.data.roomId ? (
        <p className="mt-8 text-center text-sm text-muted-foreground">Add an artist or two, and music near them shows up here.</p>
      ) : set.data.mode === 'fresh' ? (
        <FreshFeed set={set.data} />
      ) : (
        <Feed set={set.data} withFresh={set.data.mode === 'both'} />
      )}
    </>
  )
}

function useSetMutation<T>(fn: (v: T) => Promise<DiscoverySet>) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: (s) => setChanged(qc, s),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
}

function Header({ set }: { set: DiscoverySet }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(set.name)
  const rename = useSetMutation((n: string) => updateSet(set.id, { name: n }))
  const remove = useMutation({
    mutationFn: () => deleteSet(set.id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: setsQuery.queryKey, exact: true })
      void navigate({ to: '/discover', replace: true })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  return (
    <header className="flex flex-col gap-3 pt-2 pb-4">
      {editing ? (
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (!name.trim()) return
            rename.mutate(name.trim(), { onSuccess: () => setEditing(false) })
          }}
        >
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} aria-label="Set's name" maxLength={100} />
          <Button type="submit" size="icon" aria-label="Save the name" disabled={!name.trim() || rename.isPending}>
            <Check />
          </Button>
        </form>
      ) : (
        <div className="flex items-start justify-between gap-3">
          <h1 className="min-w-0 text-display break-words">{set.name}</h1>
          <div className="flex shrink-0 gap-1 pt-1">
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Rename"
              onClick={() => {
                setName(set.name)
                setEditing(true)
              }}
            >
              <Pencil />
            </Button>
            <ConfirmButton compact label="Delete set" confirmLabel="Delete" icon={<Trash2 />} pending={remove.isPending} onConfirm={() => remove.mutate()} />
          </div>
        </div>
      )}
      <SetTabs setId={set.id} on="feed" className="self-start" />
    </header>
  )
}

/** The set's artists, and a search to add more: any artist, not just yours. */
function Seeds({ set }: { set: DiscoverySet }) {
  const add = useSetMutation((name: string) => addArtist(set.id, name))
  const remove = useSetMutation((key: string) => removeArtist(set.id, key))
  const have = useMemo(() => new Set(set.artists.map((a) => a.name.toLowerCase())), [set.artists])
  const [sheet, setSheet] = useState<'starred' | 'room'>()
  return (
    <section className="glass flex flex-col gap-3 rounded-3xl p-4">
      {set.artists.length > 0 && (
        <ul className="flex flex-wrap gap-2" aria-label="Seed artists">
          {set.artists.map((a) => (
            <li
              key={a.key}
              title={sourceText(a.addedFrom) || undefined}
              className="flex items-center gap-1 rounded-full bg-primary/15 py-1 pr-1 pl-3 text-sm font-medium text-primary"
            >
              {a.name}
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label={`Take ${a.name} out`}
                disabled={remove.isPending}
                onClick={() => remove.mutate(a.key)}
                className="text-primary hover:bg-primary/15"
              >
                <X />
              </Button>
            </li>
          ))}
          {add.isPending && (
            <li className="grid place-items-center px-2">
              <LoaderCircle className="size-4 animate-spin text-muted-foreground" />
            </li>
          )}
        </ul>
      )}
      <ArtistSearch have={have} autoFocus={set.artists.length === 0} onPick={(name) => add.mutate(name)} />
      <div className="flex flex-wrap gap-2">
        <Button variant="ghost" size="sm" onClick={() => setSheet('starred')}>
          <Star data-icon="inline-start" />
          From your stars
        </Button>
        <Button variant="ghost" size="sm" onClick={() => setSheet('room')}>
          <Users data-icon="inline-start" />
          From a room
        </Button>
      </div>
      <StarredSheet set={set} open={sheet === 'starred'} onOpenChange={(o) => setSheet(o ? 'starred' : undefined)} />
      <RoomSheet set={set} open={sheet === 'room'} onOpenChange={(o) => setSheet(o ? 'room' : undefined)} />
    </section>
  )
}

/** How far the feed strays, which feeds it shows, and how far back Fresh looks. */
function Tuning({ set }: { set: DiscoverySet }) {
  // While dragging, and until the change is saved.
  const [dragged, setDragged] = useState<number>()
  const explore = dragged ?? set.explore
  const update = useSetMutation(
    (body: { explore?: number; mode?: DiscoveryMode; freshDays?: DiscoveryFreshDays; growAuto?: boolean; roomId?: string; roomWeight?: DiscoveryRoomWeight }) =>
      updateSet(set.id, body),
  )
  const rooms = useQuery(roomsQuery)
  return (
    <section className="mt-3 grid gap-4 px-1 sm:grid-cols-[1fr_auto] sm:items-center">
      <label className="flex flex-col gap-1">
        <span className="flex justify-between text-caption text-muted-foreground">
          <span>Close to the seeds</span>
          <span>Far out</span>
        </span>
        <Slider
          aria-label="How far to explore"
          min={0}
          max={100}
          step={5}
          value={[explore]}
          onValueChange={([v]) => setDragged(v)}
          onValueCommit={([v]) => update.mutate({ explore: v }, { onSettled: () => setDragged(undefined) })}
        />
      </label>
      <ToggleGroup
        type="single"
        value={set.mode}
        onValueChange={(v) => v && update.mutate({ mode: v as DiscoveryMode })}
        aria-label="Which feeds"
      >
        <ToggleGroupItem value="deep" title="Anything new to you">
          Deep cuts
        </ToggleGroupItem>
        <ToggleGroupItem value="fresh" title="Recent releases">
          Fresh
        </ToggleGroupItem>
        <ToggleGroupItem value="both">Both</ToggleGroupItem>
      </ToggleGroup>
      {set.mode !== 'deep' && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 sm:col-span-2 sm:justify-end">
          <span className="text-caption text-muted-foreground">Fresh looks back</span>
          <ToggleGroup
            type="single"
            value={String(set.freshDays)}
            onValueChange={(v) => v && update.mutate({ freshDays: Number(v) as DiscoveryFreshDays })}
            aria-label="How far back Fresh looks"
          >
            {freshWindows.map((d) => (
              <ToggleGroupItem key={d} value={String(d)}>
                {d} days
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </div>
      )}
      <div className="flex items-center justify-between gap-3 sm:col-span-2">
        <span className="min-w-0">
          <span className="block text-sm">Grow by itself</span>
          <span className="block text-caption text-muted-foreground">Artists you thumb up twice here join without asking.</span>
        </span>
        <Switch checked={set.growAuto} onChange={(on) => update.mutate({ growAuto: on })} label="Grow by itself" />
      </div>
      {(!!rooms.data?.length || set.roomId) && (
        <div className="flex flex-col gap-2 sm:col-span-2 sm:flex-row sm:items-center">
          <label className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="text-sm">Mix in a room&apos;s taste</span>
            <select
              aria-label="Mix in a room's taste"
              value={set.roomId ?? ''}
              onChange={(e) => update.mutate({ roomId: e.target.value })}
              className="h-10 w-full rounded-xl border border-input bg-transparent px-3 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <option value="">No room</option>
              {rooms.data?.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
              {set.roomId && !rooms.data?.some((r) => r.id === set.roomId) && <option value={set.roomId}>A room you left</option>}
            </select>
          </label>
          {set.roomId && (
            <ToggleGroup
              type="single"
              value={set.roomWeight}
              onValueChange={(v) => v && update.mutate({ roomWeight: v as DiscoveryRoomWeight })}
              aria-label="How much the room counts"
              className="self-end"
            >
              <ToggleGroupItem value="low">A little</ToggleGroupItem>
              <ToggleGroupItem value="medium">Some</ToggleGroupItem>
              <ToggleGroupItem value="high">A lot</ToggleGroupItem>
            </ToggleGroup>
          )}
        </div>
      )}
    </section>
  )
}

/** "Not for me": an artist out of every feed, with an undo. */
function useNotForMe(setId: string, onBlock: (artist: string) => void) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (artist: string) => block(artist),
    onMutate: (artist) => {
      tap()
      onBlock(artist)
      qc.setQueryData<DiscoveryFresh>(freshQuery(setId).queryKey, (d) =>
        d ? { ...d, items: d.items.filter((r) => r.artist.toLowerCase() !== artist.toLowerCase()) } : d,
      )
    },
    onSuccess: (b) => {
      void qc.invalidateQueries({ queryKey: blocksQuery.queryKey })
      toast({
        message: `No more ${b.name}`,
        action: {
          label: 'Undo',
          onClick: () =>
            void unblock(b.key).then(() => {
              void qc.invalidateQueries({ queryKey: blocksQuery.queryKey })
              void qc.invalidateQueries({ queryKey: feedQuery(setId).queryKey })
              void qc.invalidateQueries({ queryKey: freshQuery(setId).queryKey })
            }),
        },
      })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
}

/** The set's songs, new to you; with its Fresh releases mixed in, for "Both". */
function Feed({ set, withFresh }: { set: DiscoverySet; withFresh: boolean }) {
  const qc = useQueryClient()
  const pages = useInfiniteQuery(feedQuery(set.id))
  const fresh = useQuery({ ...freshQuery(set.id), enabled: withFresh })
  const [thumbed, setThumbed] = useState<ReadonlySet<string>>(new Set())
  const items = feedItems(pages.data?.pages)
  const first = pages.data?.pages[0]
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = pages
  const entries = mixFresh(items, withFresh ? (fresh.data?.items ?? []) : [], !hasNextPage)

  // Load the next page as the end of the list scrolls into view.
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = end.current
    if (!el || !hasNextPage) return
    const io = new IntersectionObserver(
      (es) => {
        if (es.some((e) => e.isIntersecting) && !isFetchingNextPage) void fetchNextPage()
      },
      { rootMargin: '600px' },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [hasNextPage, isFetchingNextPage, fetchNextPage])

  const refresh = useMutation({
    mutationFn: () => refreshFeed(qc, set.id),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  // Take songs out of the feed shown, as the server will from now on.
  const drop = (keep: (it: DiscoveryItem) => boolean) =>
    qc.setQueryData<InfiniteData<DiscoveryFeed, string>>(feedQuery(set.id).queryKey, (d) =>
      d ? { ...d, pages: d.pages.map((p) => ({ ...p, items: p.items.filter(keep) })) } : d,
    )

  const rate = useMutation({
    mutationFn: ({ kind, item }: { kind: 'up' | 'down'; item: DiscoveryItem }) => thumb(kind, item, set.id),
    onMutate: ({ kind, item }) => {
      tap()
      if (kind === 'down') drop((it) => it.key !== item.key)
      else setThumbed((s) => new Set(s).add(item.key))
    },
    onSuccess: (did, { kind, item }) => {
      void qc.invalidateQueries({ queryKey: suggestionsQuery(set.id).queryKey })
      if (did.want) wantChanged(qc, did.want)
      if (did.grew) void qc.invalidateQueries({ queryKey: setQuery(set.id).queryKey })
      toast({
        message:
          kind === 'down'
            ? `You won't see “${item.title}” again`
            : did.grew
              ? `“${item.title}” is on your Want list, and ${did.grew} joined this set`
              : `“${item.title}” is on your Want list`,
      })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const want = useWantRelease(set.id)
  const notForMe = useNotForMe(set.id, (artist) => drop((it) => it.artist.toLowerCase() !== artist.toLowerCase()))

  return (
    <section className="mt-8">
      <div className="mb-3 flex items-center justify-between gap-3 px-1">
        <h2 className="text-headline">New to you</h2>
        <Button variant="ghost" size="sm" disabled={refresh.isPending || pages.isPending} onClick={() => refresh.mutate()}>
          <RefreshCw data-icon="inline-start" className={refresh.isPending ? 'animate-spin' : undefined} />
          Refresh
        </Button>
      </div>
      {first?.library.building && (
        <p className="mb-3 px-1 text-caption text-muted-foreground">Still indexing your library, so a song you have might slip through.</p>
      )}
      {pages.isPending || refresh.isPending ? (
        <div className="flex flex-col gap-3">
          <p className="px-1 text-sm text-muted-foreground">Walking out from your artists…</p>
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-[5.5rem] rounded-3xl" />
          ))}
        </div>
      ) : pages.isError && !pages.data ? (
        <Notice>{errorMessage(pages.error)}</Notice>
      ) : items.length === 0 ? (
        <p className="glass rounded-3xl p-5 text-sm text-muted-foreground">
          {first?.noGraph
            ? "This server doesn't know how music relates yet: its admin can turn on Last.fm, ListenBrainz or Deezer."
            : "Nothing new turned up yet. Artists the server didn't know are being looked up; refresh in a minute, or add another artist."}
        </p>
      ) : (
        <motion.ul variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-3">
          {entries.map((e) =>
            e.kind === 'release' ? (
              <ReleaseCard
                key={`release-${e.release.key}`}
                release={e.release}
                isNew
                wanted={want.wanted.has(e.release.key)}
                onWant={() => want.mutate(e.release)}
                onBlock={() => notForMe.mutate(e.release.artist)}
              />
            ) : (
              <FeedCard
                key={e.item.key}
                item={e.item}
                thumbed={thumbed.has(e.item.key) ? 'up' : undefined}
                onThumb={(kind) => rate.mutate({ kind, item: e.item })}
                onBlock={() => notForMe.mutate(e.item.artist)}
              />
            ),
          )}
        </motion.ul>
      )}
      <div ref={end} className="flex justify-center py-6">
        {isFetchingNextPage && <LoaderCircle className="size-5 animate-spin text-muted-foreground" />}
        {!hasNextPage && items.length > 0 && <p className="text-caption text-muted-foreground">That&apos;s all for now. Refresh for another walk.</p>}
      </div>
    </section>
  )
}

/** The set's recent releases: "new releases for you". */
function FreshFeed({ set }: { set: DiscoverySet }) {
  const fresh = useQuery(freshQuery(set.id))
  const notForMe = useNotForMe(set.id, () => {})
  const want = useWantRelease(set.id)
  const data = fresh.data
  return (
    <section className="mt-8">
      <div className="mb-3 flex items-center justify-between gap-3 px-1">
        <div className="min-w-0">
          <h2 className="text-headline">Fresh</h2>
          <p className="text-caption text-muted-foreground">Out in the last {set.freshDays} days</p>
        </div>
        <Button variant="ghost" size="sm" disabled={fresh.isFetching} onClick={() => void fresh.refetch()}>
          <RefreshCw data-icon="inline-start" className={fresh.isFetching ? 'animate-spin' : undefined} />
          Refresh
        </Button>
      </div>
      {data?.library.building && (
        <p className="mb-3 px-1 text-caption text-muted-foreground">Still indexing your library, so a release you have might slip through.</p>
      )}
      {data?.partial && (data.items.length > 0 || fresh.isFetching) && (
        <p className="mb-3 flex items-center gap-1.5 px-1 text-caption text-muted-foreground">
          <LoaderCircle className="size-3 animate-spin" />
          Still looking up some artists&apos; releases
        </p>
      )}
      {fresh.isPending ? (
        <div className="flex flex-col gap-3">
          <p className="px-1 text-sm text-muted-foreground">Looking for what came out lately…</p>
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-[5.5rem] rounded-3xl" />
          ))}
        </div>
      ) : fresh.isError && !data ? (
        <Notice>{errorMessage(fresh.error)}</Notice>
      ) : !data || data.items.length === 0 ? (
        <p className="glass rounded-3xl p-5 text-sm text-muted-foreground">
          {data?.noReleases
            ? "This server has no source of new releases: its admin can turn on Deezer, ListenBrainz or MusicBrainz."
            : data?.partial
              ? "Looking up your artists' releases. They'll show up here shortly."
              : `Nothing new from your artists or their neighbors in the last ${set.freshDays} days. Look further back, or add another artist.`}
        </p>
      ) : (
        <motion.ul variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-3">
          {data.items.map((r) => (
            <ReleaseCard
              key={r.key}
              release={r}
              wanted={want.wanted.has(r.key)}
              onWant={() => want.mutate(r)}
              onBlock={() => notForMe.mutate(r.artist)}
            />
          ))}
        </motion.ul>
      )}
    </section>
  )
}

/** "Want" on a release: onto the Want list, until it's in your library. */
function useWantRelease(setId: string) {
  const qc = useQueryClient()
  const [wanted, setWanted] = useState<ReadonlySet<string>>(new Set())
  const m = useMutation({
    mutationFn: (r: DiscoveryRelease) => wantRelease(r, setId),
    onMutate: (r) => {
      tap()
      setWanted((s) => new Set(s).add(r.key))
    },
    onSuccess: (item) => {
      wantChanged(qc, item)
      toast({ message: `“${item.title}” is on your Want list` })
    },
    onError: (e, r) => {
      setWanted((s) => {
        const next = new Set(s)
        next.delete(r.key)
        return next
      })
      toast({ message: errorMessage(e), tone: 'error' })
    },
  })
  return { mutate: m.mutate, wanted }
}
