import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Compass, LoaderCircle, Plus, RefreshCw, X } from 'lucide-react'
import { motion } from 'motion/react'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { useMe } from '@/lib/auth'
import {
  blocksQuery,
  createSet,
  libraryQuery,
  refreshLibrary,
  setChanged,
  setsQuery,
  unblock,
  type DiscoveryLibrary,
  type DiscoverySetSummary,
} from '@/lib/discover'
import { fadeUp, stagger } from '@/lib/motion'
import { relativeTime } from '@/lib/time'
import { toast } from '@/lib/toast'

export const Route = createFileRoute('/_app/_authed/discover/')({
  loader: ({ context }) => context.queryClient.prefetchQuery(setsQuery),
  component: Discover,
})

const modeLabel = { deep: 'Deep cuts', fresh: 'Fresh', both: 'Deep cuts and fresh' } as const

/** Your discovery sets: artists you pick, each a feed of music you don't have yet. */
function Discover() {
  const me = useMe()
  const sets = useQuery({ ...setsQuery, enabled: !me.guest })
  const [naming, setNaming] = useState(false)

  if (me.guest) {
    return (
      <>
        <PageHeader title="Discover" />
        <p className="glass rounded-3xl p-5 text-sm text-muted-foreground">Discover is for members. Guests can queue in the room they joined.</p>
      </>
    )
  }
  const all = sets.data ?? []
  return (
    <>
      <PageHeader
        title="Discover"
        subtitle="Music you don't have yet, near artists you pick"
        actions={
          <Button variant="glass" size="icon" aria-label="New set" onClick={() => setNaming((n) => !n)}>
            <Plus className="size-5" />
          </Button>
        }
      />
      {naming && <NewSet onDone={() => setNaming(false)} />}
      {sets.isPending ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-24 rounded-3xl" />
          ))}
        </div>
      ) : all.length === 0 ? (
        <div className="glass flex flex-col items-start gap-3 rounded-3xl p-5 text-sm text-muted-foreground">
          <p>
            Make a set of artists you love, like &ldquo;Moody post-rock&rdquo; or &ldquo;90s alt&rdquo;, and Discover finds songs near them that
            aren&apos;t in your library. Listen to a preview, then find the whole song on YouTube.
          </p>
          {!naming && (
            <Button size="sm" onClick={() => setNaming(true)}>
              <Plus data-icon="inline-start" />
              New set
            </Button>
          )}
        </div>
      ) : (
        <motion.ul variants={stagger} initial="hidden" animate="show" className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {all.map((s) => (
            <motion.li key={s.id} variants={fadeUp}>
              <SetCard set={s} />
            </motion.li>
          ))}
        </motion.ul>
      )}
      <LibraryStatus />
      <Blocks />
    </>
  )
}

function NewSet({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const create = useMutation({
    mutationFn: () => createSet(name.trim()),
    onSuccess: (s) => {
      setChanged(qc, s)
      onDone()
      void navigate({ to: '/discover/$setId', params: { setId: s.id } })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  return (
    <motion.form
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: 0 }}
      className="glass mb-6 flex gap-2 rounded-3xl p-3"
      onSubmit={(e) => {
        e.preventDefault()
        if (name.trim()) create.mutate()
      }}
    >
      <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Name it: Moody post-rock" aria-label="New set's name" maxLength={100} />
      <Button type="submit" disabled={!name.trim() || create.isPending}>
        Create
      </Button>
    </motion.form>
  )
}

function SetCard({ set: s }: { set: DiscoverySetSummary }) {
  return (
    <Link
      to="/discover/$setId"
      params={{ setId: s.id }}
      className="glass flex items-center gap-4 rounded-3xl p-4 transition-transform outline-none hover:scale-[1.01] focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span className="grid size-14 shrink-0 place-items-center rounded-2xl bg-primary/15 text-primary">
        <Compass className="size-6" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium">{s.name}</span>
        <span className="block truncate text-caption text-muted-foreground">
          {s.artistCount} artist{s.artistCount === 1 ? '' : 's'} · {modeLabel[s.mode]}
        </span>
      </span>
    </Link>
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
    <section className="mt-10">
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

/** Artists you said aren't for you. */
function Blocks() {
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
  if (!blocks.data || blocks.data.length === 0) return null
  return (
    <section className="mt-10">
      <h2 className="mb-2 px-1 text-headline">Not for me</h2>
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
