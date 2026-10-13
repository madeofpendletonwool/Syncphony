import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { ChevronRight, Crosshair, LoaderCircle, RefreshCw } from 'lucide-react'
import { Fragment, useEffect, useMemo, useState } from 'react'
import { errorMessage } from '@/api/errors'
import { BackButton } from '@/components/back-button'
import { AddToSetSheet } from '@/components/discover/add-to-set'
import { ArtistSearch } from '@/components/discover/artist-search'
import { MapArtistSheet } from '@/components/discover/map-artist-sheet'
import { MapGraph } from '@/components/discover/map-graph'
import { SetTabs } from '@/components/discover/set-tabs'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { useMe } from '@/lib/auth'
import {
  addArtist,
  block,
  blocksQuery,
  mapQuery,
  setChanged,
  setQuery,
  stopPreview,
  unblock,
  type DiscoveryMapNode,
  type MapOf,
} from '@/lib/discover'
import { crumbs } from '@/lib/discover-map'
import { tap } from '@/lib/haptics'
import { toast } from '@/lib/toast'

type Search = {
  /** The set the map started from. */
  set?: string
  /** The artists re-centered on, in order: the map is of the last. */
  path?: string[]
}

export const Route = createFileRoute('/_app/_authed/discover/map')({
  validateSearch: (search: Record<string, unknown>): Search => ({
    set: typeof search.set === 'string' && search.set ? search.set : undefined,
    path: Array.isArray(search.path) ? search.path.filter((p): p is string => typeof p === 'string' && p.trim() !== '').slice(-30) : undefined,
  }),
  component: MapPage,
})

/**
 * The discovery map (MAD-821): artists around a set's seeds, or around any
 * artist, in the spirit of music-map.com. Tap an artist to hear their
 * songs, or re-center on them and wander; Back retraces the way.
 */
function MapPage() {
  const me = useMe()
  const { set: setId, path = [] } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const center = path.at(-1)
  const of: MapOf | undefined = center ? { artist: center } : setId ? { setId } : undefined
  const set = useQuery({ ...setQuery(setId ?? ''), enabled: !!setId && !me.guest })
  // A preview stops when you leave.
  useEffect(() => stopPreview, [])

  const go = (next: string[]) => {
    tap()
    void navigate({ search: { set: setId, path: next.length ? next : undefined } })
  }

  if (me.guest) {
    return (
      <>
        <BackButton fallback="/discover" />
        <p className="glass mt-4 rounded-3xl p-5 text-sm text-muted-foreground">Discover is for members.</p>
      </>
    )
  }
  const trail = crumbs(setId ? (set.data?.name ?? '…') : undefined, path)
  return (
    <>
      <BackButton fallback="/discover" />
      <header className="flex flex-col gap-3 pt-2 pb-4">
        <h1 className="min-w-0 text-display break-words">{center ?? set.data?.name ?? 'Map'}</h1>
        {setId && <SetTabs setId={setId} on="map" className="self-start" />}
      </header>
      <ArtistSearch
        have={new Set(center ? [center.toLowerCase()] : [])}
        placeholder="Start from any artist"
        icon={<Crosshair className="size-4" />}
        onPick={(name) => go([...path, name])}
      />
      {trail.length > 1 && (
        <nav aria-label="Where you've been" className="mt-4 flex flex-wrap items-center gap-1 px-1 text-sm">
          {trail.map((c, i) => (
            <Fragment key={`${i}-${c.label}`}>
              {i > 0 && <ChevronRight className="size-3.5 text-muted-foreground" />}
              {i === trail.length - 1 ? (
                <span aria-current="location" className="font-medium">
                  {c.label}
                </span>
              ) : (
                <button type="button" onClick={() => go(c.path)} className="rounded text-muted-foreground hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none">
                  {c.label}
                </button>
              )}
            </Fragment>
          ))}
        </nav>
      )}
      {of ? (
        <MapView of={of} setId={setId} setName={set.data?.name} onRecenter={(name) => go([...path, name])} />
      ) : (
        <p className="glass mt-6 rounded-3xl p-5 text-sm text-muted-foreground">
          Search for an artist, any artist, and see who&apos;s near them. Tap one to hear their songs, or re-center on them and keep going.
        </p>
      )}
    </>
  )
}

function MapView({ of, setId, setName, onRecenter }: { of: MapOf; setId?: string; setName?: string; onRecenter: (name: string) => void }) {
  const qc = useQueryClient()
  const map = useQuery(mapQuery(of))
  const [open, setOpen] = useState<DiscoveryMapNode>()
  const [adding, setAdding] = useState<string>()
  const data = map.data

  const addToThis = useMutation({
    mutationFn: (n: DiscoveryMapNode) => addArtist(setId!, n.name),
    onMutate: () => tap(),
    onSuccess: (s, n) => {
      setChanged(qc, s)
      toast({ message: `Added ${n.name} to ${s.name}` })
      setOpen(undefined)
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const notForMe = useMutation({
    mutationFn: (n: DiscoveryMapNode) => block(n.name),
    onMutate: () => {
      tap()
      setOpen(undefined)
    },
    onSuccess: (b) => {
      void qc.invalidateQueries({ queryKey: blocksQuery.queryKey })
      void qc.invalidateQueries({ queryKey: ['discover', 'map'] })
      qc.removeQueries({ queryKey: ['discover', 'feed'] })
      toast({
        message: `No more ${b.name}`,
        action: {
          label: 'Undo',
          onClick: () =>
            void unblock(b.key).then(() => {
              void qc.invalidateQueries({ queryKey: blocksQuery.queryKey })
              void qc.invalidateQueries({ queryKey: ['discover', 'map'] })
              qc.removeQueries({ queryKey: ['discover', 'feed'] })
            }),
        },
      })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  const mine = useMemo(() => (data?.nodes ?? []).filter((n) => n.owned).length, [data])

  return (
    <section className="mt-4">
      <div className="glass relative aspect-square max-h-[70dvh] w-full overflow-hidden rounded-3xl sm:aspect-[4/3]">
        {map.isPending ? (
          <div className="grid h-full place-items-center">
            <div className="flex flex-col items-center gap-3 text-sm text-muted-foreground">
              <LoaderCircle className="size-6 animate-spin" />
              Mapping who&apos;s near…
            </div>
          </div>
        ) : map.isError && !data ? (
          <div className="grid h-full place-items-center p-6">
            <Notice>{errorMessage(map.error)}</Notice>
          </div>
        ) : data && data.nodes.length > 1 ? (
          <MapGraph map={data} selected={open?.key} onSelect={(n) => setOpen(n)} className="p-2" />
        ) : (
          <div className="grid h-full place-items-center p-6 text-center text-sm text-muted-foreground">
            {data?.noGraph
              ? "This server doesn't know how music relates yet: its admin can turn on Last.fm, ListenBrainz or Deezer."
              : data?.set && data.set.artists.length === 0
                ? 'Add an artist or two to this set, and the map grows around them.'
                : data?.partial
                  ? 'Looking them up. The map fills in shortly.'
                  : 'Nobody known turned up near here.'}
          </div>
        )}
        {data?.partial && (
          <span className="glass absolute top-3 right-3 flex items-center gap-1.5 rounded-full px-3 py-1 text-caption text-muted-foreground">
            <LoaderCircle className="size-3 animate-spin" />
            Filling in
          </span>
        )}
      </div>
      {data && data.nodes.length > 1 && (
        <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 px-1 text-caption text-muted-foreground">
          <Legend kind="new">New to you</Legend>
          <Legend kind="owned">{`In your library (${mine})`}</Legend>
          <Legend kind="ring">{data.set ? 'Seed' : 'Center'}</Legend>
          {data.set && <Legend kind="halo">Near several seeds</Legend>}
          <Button variant="ghost" size="sm" className="ml-auto" disabled={map.isFetching} onClick={() => void map.refetch()}>
            <RefreshCw data-icon="inline-start" className={map.isFetching ? 'animate-spin' : undefined} />
            Reload
          </Button>
        </div>
      )}
      {data?.library.building && (
        <p className="mt-2 px-1 text-caption text-muted-foreground">Still indexing your library, so an artist you have might not be dimmed yet.</p>
      )}
      <MapArtistSheet
        node={open}
        onOpenChange={(o) => !o && setOpen(undefined)}
        onRecenter={(n) => {
          setOpen(undefined)
          onRecenter(n.name)
        }}
        onAddToSet={(n) => {
          setOpen(undefined)
          setAdding(n.name)
        }}
        addTo={setId && setName ? { name: setName, add: (n) => addToThis.mutate(n), pending: addToThis.isPending } : undefined}
        onBlock={(n) => notForMe.mutate(n)}
      />
      <AddToSetSheet artist={adding ?? ''} open={!!adding} onOpenChange={(o) => !o && setAdding(undefined)} />
    </section>
  )
}

function Legend({ kind, children }: { kind: 'new' | 'owned' | 'ring' | 'halo'; children: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <svg viewBox="-8 -8 16 16" className="size-3.5" aria-hidden>
        {kind === 'halo' && <circle r={7.5} fill="var(--primary)" fillOpacity={0.15} />}
        {kind === 'ring' && <circle r={6.5} fill="none" stroke="var(--primary)" strokeWidth={1.5} />}
        <circle r={kind === 'ring' ? 3.5 : 4.5} fill={kind === 'owned' ? 'var(--muted-foreground)' : 'var(--primary)'} fillOpacity={kind === 'owned' ? 0.35 : 0.95} />
      </svg>
      {children}
    </span>
  )
}
