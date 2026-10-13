import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { Check, LoaderCircle, Pencil, RefreshCw, Trash2, X } from 'lucide-react'
import { motion } from 'motion/react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { errorMessage } from '@/api/errors'
import { BackButton } from '@/components/back-button'
import { ConfirmButton } from '@/components/confirm-button'
import { ArtistSearch } from '@/components/discover/artist-search'
import { FeedCard } from '@/components/discover/feed-card'
import { SetTabs } from '@/components/discover/set-tabs'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Slider } from '@/components/ui/slider'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  addArtist,
  block,
  blocksQuery,
  deleteSet,
  feedItems,
  feedQuery,
  refreshFeed,
  removeArtist,
  setChanged,
  setQuery,
  setsQuery,
  stopPreview,
  thumb,
  unblock,
  updateSet,
  type DiscoveryFeed,
  type DiscoveryItem,
  type DiscoveryMode,
  type DiscoverySet,
} from '@/lib/discover'
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
      {set.data.artists.length === 0 ? (
        <p className="mt-8 text-center text-sm text-muted-foreground">Add an artist or two, and songs near them show up here.</p>
      ) : (
        <Feed set={set.data} />
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
  return (
    <section className="glass flex flex-col gap-3 rounded-3xl p-4">
      {set.artists.length > 0 && (
        <ul className="flex flex-wrap gap-2" aria-label="Seed artists">
          {set.artists.map((a) => (
            <li key={a.key} className="flex items-center gap-1 rounded-full bg-primary/15 py-1 pr-1 pl-3 text-sm font-medium text-primary">
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
    </section>
  )
}

/** How far the feed strays, and which feeds it shows. */
function Tuning({ set }: { set: DiscoverySet }) {
  // While dragging, and until the change is saved.
  const [dragged, setDragged] = useState<number>()
  const explore = dragged ?? set.explore
  const update = useSetMutation((body: { explore?: number; mode?: DiscoveryMode }) => updateSet(set.id, body))
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
        <ToggleGroupItem value="deep">Deep cuts</ToggleGroupItem>
        <ToggleGroupItem value="fresh" title="New releases arrive in a later update">
          Fresh
        </ToggleGroupItem>
        <ToggleGroupItem value="both">Both</ToggleGroupItem>
      </ToggleGroup>
    </section>
  )
}

function Feed({ set }: { set: DiscoverySet }) {
  const qc = useQueryClient()
  const pages = useInfiniteQuery(feedQuery(set.id))
  const [thumbed, setThumbed] = useState<ReadonlySet<string>>(new Set())
  const items = feedItems(pages.data?.pages)
  const first = pages.data?.pages[0]
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = pages

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
    onSuccess: (_, { kind, item }) =>
      toast({ message: kind === 'up' ? `Glad you like “${item.title}”` : `You won't see “${item.title}” again` }),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const notForMe = useMutation({
    mutationFn: (item: DiscoveryItem) => block(item.artist),
    onMutate: (item) => {
      tap()
      drop((it) => it.artist.toLowerCase() !== item.artist.toLowerCase())
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
              void qc.invalidateQueries({ queryKey: feedQuery(set.id).queryKey })
            }),
        },
      })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

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
          {items.map((it) => (
            <FeedCard
              key={it.key}
              item={it}
              thumbed={thumbed.has(it.key) ? 'up' : undefined}
              onThumb={(kind) => rate.mutate({ kind, item: it })}
              onBlock={() => notForMe.mutate(it)}
            />
          ))}
        </motion.ul>
      )}
      <div ref={end} className="flex justify-center py-6">
        {isFetchingNextPage && <LoaderCircle className="size-5 animate-spin text-muted-foreground" />}
        {!hasNextPage && items.length > 0 && <p className="text-caption text-muted-foreground">That&apos;s all for now. Refresh for another walk.</p>}
      </div>
    </section>
  )
}
