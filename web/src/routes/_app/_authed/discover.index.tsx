import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Compass, Crosshair, Heart, Plus, Settings2, Users } from 'lucide-react'
import { motion } from 'motion/react'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { ArtistSearch } from '@/components/discover/artist-search'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { useMe } from '@/lib/auth'
import { RoomSheet } from '@/components/discover/import-sheets'
import { Notice } from '@/components/notice'
import { createSet, landedText, setChanged, setsQuery, wantQuery, yieldText, type DiscoverySetSummary } from '@/lib/discover'
import { fadeUp, stagger } from '@/lib/motion'
import { roomsQuery } from '@/lib/room'
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
  const [fromRoom, setFromRoom] = useState(false)

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
          <>
            <Button asChild variant="ghost" size="icon" aria-label="Discover settings">
              <Link to="/discover/settings">
                <Settings2 className="size-5" />
              </Link>
            </Button>
            <Button variant="glass" size="icon" aria-label="New set" onClick={() => setNaming((n) => !n)}>
              <Plus className="size-5" />
            </Button>
          </>
        }
      />
      <Landed />
      {naming && <NewSet onDone={() => setNaming(false)} onFromRoom={() => setFromRoom(true)} />}
      <RoomSheet open={fromRoom} onOpenChange={setFromRoom} />
      <WantCard />
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
      <MapStart />
    </>
  )
}

function NewSet({ onDone, onFromRoom }: { onDone: () => void; onFromRoom: () => void }) {
  const rooms = useQuery(roomsQuery)
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
      {!!rooms.data?.length && (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label="Make a set from a room's taste"
          title="From a room's taste"
          onClick={() => {
            onDone()
            onFromRoom()
          }}
        >
          <Users />
        </Button>
      )}
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
          {s.artistCount} artist{s.artistCount === 1 ? '' : 's'}
          {s.roomId ? ' and a room' : ''} · {modeLabel[s.mode]}
        </span>
        {yieldText(s) && <span className="block truncate text-caption text-primary">{yieldText(s)}</span>}
      </span>
    </Link>
  )
}

/** The map, from any artist: how you explore something entirely new. */
function MapStart() {
  const navigate = useNavigate()
  return (
    <section className="mt-10">
      <h2 className="mb-1 px-1 text-headline">Map</h2>
      <p className="mb-3 px-1 text-sm text-muted-foreground">Start from any artist and wander: tap one to hear them, or re-center and keep going.</p>
      <ArtistSearch
        have={new Set()}
        placeholder="Map an artist"
        icon={<Crosshair className="size-4" />}
        onPick={(name) => void navigate({ to: '/discover/map', search: { path: [name] } })}
      />
    </section>
  )
}

/** "3 wanted things landed in your library": the Want list's news. */
function Landed() {
  const want = useQuery(wantQuery)
  const n = want.data?.landed ?? 0
  return (
    <Notice tone="success" className="mb-4">
      {n > 0 && (
        <Link to="/discover/want" search={{ show: 'got' }} className="underline-offset-4 hover:underline">
          {landedText(n)}. See them
        </Link>
      )}
    </Notice>
  )
}

/** The Want list: what you liked, until it's in your library. */
function WantCard() {
  const want = useQuery(wantQuery)
  const wanted = want.data?.items.filter((w) => w.status === 'wanted').length ?? 0
  const got = want.data?.items.filter((w) => w.status === 'got').length ?? 0
  if (!want.data || want.data.items.length === 0) return null
  return (
    <Link
      to="/discover/want"
      className="glass mb-6 flex items-center gap-4 rounded-3xl p-4 transition-transform outline-none hover:scale-[1.01] focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <span className="grid size-14 shrink-0 place-items-center rounded-2xl bg-primary/15 text-primary">
        <Heart className="size-6" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium">Want list</span>
        <span className="block truncate text-caption text-muted-foreground">
          {wanted} wanted · {got} got
        </span>
      </span>
    </Link>
  )
}
