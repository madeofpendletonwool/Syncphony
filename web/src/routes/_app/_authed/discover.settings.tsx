import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { LoaderCircle, RefreshCw, X } from 'lucide-react'
import { errorMessage } from '@/api/errors'
import { BackButton } from '@/components/back-button'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { blocksQuery, libraryQuery, refreshLibrary, unblock, type DiscoveryLibrary } from '@/lib/discover'
import { relativeTime } from '@/lib/time'
import { toast } from '@/lib/toast'

export const Route = createFileRoute('/_app/_authed/discover/settings')({
  component: DiscoverSettings,
})

/** What Discover leaves out: your library, and artists you said aren't for you. */
function DiscoverSettings() {
  const blocks = useQuery(blocksQuery)
  return (
    <>
      <BackButton fallback="/discover" />
      <PageHeader title="Discover settings" />
      <LibraryStatus />
      <Blocks empty={blocks.data?.length === 0} />
    </>
  )
}

/** What's left out: the songs on your own Navidrome, indexed in the background. */
function LibraryStatus() {
  const qc = useQueryClient()
  const lib = useQuery(libraryQuery)
  const again = useMutation({
    mutationFn: refreshLibrary,
    onSuccess: (l) => qc.setQueryData(libraryQuery.queryKey, l),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  if (!lib.data) return null
  return (
    <section>
      <h2 className="mb-2 px-1 text-headline">Your library</h2>
      <div className="glass flex items-center gap-3 rounded-3xl p-4 text-sm">
        <p className="min-w-0 flex-1 text-muted-foreground">{libraryText(lib.data)}</p>
        {lib.data.links > 0 || lib.data.building ? (
          <Button variant="ghost" size="sm" disabled={lib.data.building || again.isPending} onClick={() => again.mutate()}>
            {lib.data.building ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : <RefreshCw data-icon="inline-start" />}
            {lib.data.building ? 'Indexing' : 'Index again'}
          </Button>
        ) : null}
      </div>
    </section>
  )
}

function libraryText(l: DiscoveryLibrary) {
  if (l.links === 0 && !l.building) {
    return "No Navidrome of yours is linked, so nothing is left out. Link one in Settings → Services and Discover skips what's on it."
  }
  if (l.links === 0) return 'Indexing your library, so Discover can skip what you have.'
  const when = l.builtAt ? `, as of ${relativeTime(l.builtAt)}` : ''
  return `Skipping the ${l.songs.toLocaleString()} songs on your Navidrome${when}.`
}

/** Artists you said aren't for you: blocked from every feed, until you let them back. */
function Blocks({ empty }: { empty: boolean }) {
  const qc = useQueryClient()
  const blocks = useQuery(blocksQuery)
  const undo = useMutation({
    mutationFn: unblock,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: blocksQuery.queryKey })
      qc.removeQueries({ queryKey: ['discover', 'feed'] })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  if (!blocks.data) return null
  return (
    <section className="mt-10">
      <h2 className="mb-1 px-1 text-headline">Not for me</h2>
      <p className="mb-3 px-1 text-sm text-muted-foreground">
        {empty ? "Nobody's blocked. Tap the not-for-me button on a song, and its artist stays out of all your feeds." : 'Kept out of all your feeds. Let one back in with ×.'}
      </p>
      <ul className="flex flex-wrap gap-2">
        {blocks.data.map((b) => (
          <li key={b.key} className="glass flex items-center gap-1 rounded-full py-1 pr-1 pl-3 text-sm">
            {b.name}
            <Button variant="ghost" size="icon-xs" aria-label={`Let ${b.name} back in`} disabled={undo.isPending} onClick={() => undo.mutate(b.key)}>
              <X />
            </Button>
          </li>
        ))}
      </ul>
    </section>
  )
}
