import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { Check, ExternalLink, LoaderCircle, Pause, Play, Search, Undo2, X } from 'lucide-react'
import { motion } from 'motion/react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { errorMessage } from '@/api/errors'
import { Artwork } from '@/components/artwork'
import { BackButton } from '@/components/back-button'
import { AlbumLink, ArtistLink } from '@/components/linked-names'
import { Notice } from '@/components/notice'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import {
  groupWants,
  imageUrl,
  landedText,
  releaseCoverUrl,
  releaseYoutubeUrl,
  seeLanded,
  setsQuery,
  setWant,
  stopPreview,
  togglePreview,
  usePreview,
  wantChanged,
  wantQuery,
  youtubeUrl,
  type WantItem,
  type WantStatus,
} from '@/lib/discover'
import { tap } from '@/lib/haptics'
import { fadeUp, stagger } from '@/lib/motion'
import { relativeTime } from '@/lib/time'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'

const shows = ['got', 'dismissed'] as const
type Show = (typeof shows)[number]

export const Route = createFileRoute('/_app/_authed/discover/want')({
  validateSearch: (search: Record<string, unknown>): { show?: Show } =>
    shows.includes(search.show as Show) ? { show: search.show as Show } : {},
  loader: ({ context }) => context.queryClient.prefetchQuery(wantQuery),
  component: WantPage,
})

const empty: Record<WantStatus, string> = {
  wanted: "Nothing wanted yet. Thumb up a song in Discover, or want a release from Fresh, and it waits here until it's in your library.",
  got: 'Nothing got yet. Add something you wanted to your Navidrome, and it shows up here after the next index.',
  dismissed: 'Nothing dismissed.',
}

/** Songs and releases you liked in Discover, until they turn up in your library (MAD-824). */
function WantPage() {
  const qc = useQueryClient()
  const navigate = Route.useNavigate()
  const { show } = Route.useSearch()
  const status: WantStatus = show ?? 'wanted'
  const list = useQuery(wantQuery)
  const sets = useQuery(setsQuery)
  const setNames = useMemo(() => new Map(sets.data?.map((s) => [s.id, s.name])), [sets.data])

  // What landed since you last looked: shown once, then noted as seen.
  const [landed, setLanded] = useState(0)
  const seen = useRef(false)
  useEffect(() => {
    const n = list.data?.landed ?? 0
    if (n === 0 || seen.current) return
    seen.current = true
    setLanded(n)
    void seeLanded().then(() => qc.setQueryData(wantQuery.queryKey, (l: typeof list.data) => (l ? { ...l, landed: 0 } : l)))
  }, [list.data, qc])
  useEffect(() => stopPreview, [])

  const change = useMutation({
    mutationFn: ({ item, to }: { item: WantItem; to: WantStatus }) => setWant(item.id, to),
    onMutate: () => tap(),
    onSuccess: (item, { to }) => {
      wantChanged(qc, item)
      if (to === 'got') toast({ message: `Got “${item.title}”` })
      if (to === 'dismissed') {
        toast({ message: `Dismissed “${item.title}”`, action: { label: 'Undo', onClick: () => change.mutate({ item, to: 'wanted' }) } })
      }
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  const items = list.data?.items ?? []
  const counts = { wanted: 0, got: 0, dismissed: 0 }
  for (const it of items) counts[it.status]++
  const groups = groupWants(items, status)

  return (
    <>
      <BackButton fallback="/discover" />
      <PageHeader title="Want list" subtitle="What you liked in Discover, until it's in your library" />
      {landed > 0 && (
        <Notice tone="success" className="mb-4">
          {landedText(landed)}. They&apos;re under Got, ready to play.
        </Notice>
      )}
      <ToggleGroup
        type="single"
        value={status}
        onValueChange={(v) => v && void navigate({ search: v === 'wanted' ? {} : { show: v as Show }, replace: true })}
        aria-label="Show"
        className="mb-5"
      >
        <ToggleGroupItem value="wanted">Wanted {counts.wanted > 0 && <span className="text-muted-foreground">{counts.wanted}</span>}</ToggleGroupItem>
        <ToggleGroupItem value="got">Got {counts.got > 0 && <span className="text-muted-foreground">{counts.got}</span>}</ToggleGroupItem>
        <ToggleGroupItem value="dismissed">Dismissed</ToggleGroupItem>
      </ToggleGroup>
      {list.isPending ? (
        <div className="flex flex-col gap-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-20 rounded-3xl" />
          ))}
        </div>
      ) : list.isError ? (
        <Notice>{errorMessage(list.error)}</Notice>
      ) : groups.length === 0 ? (
        <p className="glass rounded-3xl p-5 text-sm text-muted-foreground">{empty[status]}</p>
      ) : (
        <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-6">
          {groups.map((g) => (
            <motion.section key={g.artist} variants={fadeUp}>
              <h2 className="mb-2 px-1 text-headline">
                <ArtistLink name={g.artist} />
              </h2>
              <ul className="glass flex flex-col rounded-3xl p-1">
                {g.items.map((it) => (
                  <WantRow
                    key={it.id}
                    item={it}
                    setName={it.setId ? setNames.get(it.setId) : undefined}
                    busy={change.isPending && change.variables?.item.id === it.id}
                    onChange={(to) => change.mutate({ item: it, to })}
                  />
                ))}
              </ul>
            </motion.section>
          ))}
        </motion.div>
      )}
    </>
  )
}

function WantRow({ item, setName, busy, onChange }: { item: WantItem; setName?: string; busy: boolean; onChange: (to: WantStatus) => void }) {
  const p = usePreview(item.id)
  const isTrack = item.kind === 'track'
  const canPlay = isTrack && !!item.deezerId && !p.failed && item.status !== 'got'
  const cover = isTrack ? imageUrl('cover', item.cover, 120) : releaseCoverUrl({ cover: item.cover, mbid: item.mbid }, 120)
  const youtube = isTrack ? youtubeUrl(item) : releaseYoutubeUrl({ artist: item.artist, title: item.title, kind: 'album' })
  const detail = [isTrack ? item.release : 'Release', setName && `from ${setName}`].filter(Boolean).join(' · ')
  return (
    <li className="flex flex-col gap-2 rounded-[1.25rem] p-2 sm:flex-row sm:items-center">
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <button
          type="button"
          disabled={!canPlay}
          onClick={() => togglePreview({ key: item.id, deezerId: item.deezerId })}
          aria-label={!canPlay ? item.title : p.playing ? `Pause ${item.title}` : `Play a preview of ${item.title}`}
          className="group relative shrink-0 rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-default"
        >
          <Artwork src={cover} className="size-12 rounded-lg shadow-none" />
          {canPlay && (
            <span
              className={cn(
                'absolute inset-0 grid place-items-center rounded-lg bg-black/35 text-white transition-opacity',
                p.playing || p.loading ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 [@media(hover:none)]:opacity-100',
              )}
            >
              {p.loading ? <LoaderCircle className="size-4 animate-spin" /> : p.playing ? <Pause className="size-4 fill-current" /> : <Play className="size-4 fill-current" />}
            </span>
          )}
        </button>
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">
            {item.status === 'got' && !isTrack ? <AlbumLink title={item.title} artist={item.artist} /> : item.title}
          </p>
          <p className="truncate text-caption text-muted-foreground">
            {item.status === 'got' && item.gotAt
              ? `${item.gotBy === 'auto' ? 'Landed in your library' : 'Got'} ${relativeTime(item.gotAt)}`
              : detail}
          </p>
        </div>
      </div>
      <div className="flex items-center justify-end gap-1">
        {item.status === 'got' ? (
          <>
            {isTrack && (
              <Button asChild variant="glass" size="sm">
                <Link to="/search" search={{ q: `${item.artist} ${item.title}`, tab: 'songs' }}>
                  <Search data-icon="inline-start" />
                  Find it
                </Link>
              </Button>
            )}
            <Button variant="ghost" size="icon-sm" aria-label={`${item.title}: not got yet`} title="Not got yet" disabled={busy} onClick={() => onChange('wanted')}>
              <Undo2 />
            </Button>
          </>
        ) : item.status === 'dismissed' ? (
          <Button variant="ghost" size="sm" disabled={busy} onClick={() => onChange('wanted')}>
            <Undo2 data-icon="inline-start" />
            Want again
          </Button>
        ) : (
          <>
            <Button asChild variant="ghost" size="icon-sm">
              <a href={youtube} target="_blank" rel="noopener noreferrer" aria-label={`Open ${item.title} on YouTube`} title="Open on YouTube">
                <ExternalLink />
              </a>
            </Button>
            <Button variant="glass" size="sm" disabled={busy} onClick={() => onChange('got')}>
              <Check data-icon="inline-start" />
              Got it
            </Button>
            <Button variant="ghost" size="icon-sm" aria-label={`Dismiss ${item.title}`} title="Dismiss" disabled={busy} onClick={() => onChange('dismissed')}>
              <X />
            </Button>
          </>
        )}
      </div>
    </li>
  )
}
