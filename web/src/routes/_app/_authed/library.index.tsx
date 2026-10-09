import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Moon, Plus, Users } from 'lucide-react'
import { motion } from 'motion/react'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { PageHeader } from '@/components/page-header'
import { PlaylistCover } from '@/components/playlists/playlist-cover'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { useMe } from '@/lib/auth'
import { fadeUp, stagger } from '@/lib/motion'
import { createPlaylist, playlistChanged, savedPlaylistsQuery, type SavedPlaylistSummary } from '@/lib/playlists'
import { roomsQuery } from '@/lib/room'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'

export const Route = createFileRoute('/_app/_authed/library/')({
  loader: ({ context }) => context.queryClient.prefetchQuery(savedPlaylistsQuery),
  component: Library,
})

/** Your Syncphony playlists, and the ones friends shared with your rooms. */
function Library() {
  const me = useMe()
  const lists = useQuery(savedPlaylistsQuery)
  const [naming, setNaming] = useState(false)
  const all = lists.data ?? []
  const mine = all.filter((p) => p.ownerId === me.id)
  const shared = all.filter((p) => p.ownerId !== me.id)

  return (
    <>
      <PageHeader
        title="Library"
        subtitle="Playlists kept in Syncphony, from any service"
        actions={
          <Button variant="glass" size="icon" aria-label="New playlist" onClick={() => setNaming((n) => !n)}>
            <Plus className="size-5" />
          </Button>
        }
      />
      {naming && <NewPlaylist onDone={() => setNaming(false)} />}
      {lists.isPending ? (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="aspect-square rounded-3xl" />
          ))}
        </div>
      ) : all.length === 0 ? (
        <div className="glass flex flex-col items-start gap-3 rounded-3xl p-5 text-sm text-muted-foreground">
          <p>
            No playlists yet. Save a night from its recap in <Link to="/history" className="text-primary">History</Link>, or save songs
            as you find them with the bookmark next to them.
          </p>
          {!naming && (
            <Button size="sm" onClick={() => setNaming(true)}>
              <Plus data-icon="inline-start" />
              New playlist
            </Button>
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-8">
          {mine.length > 0 && <Shelf title={shared.length > 0 ? 'Yours' : undefined} lists={mine} />}
          {shared.length > 0 && <Shelf title="Shared with your rooms" lists={shared} />}
        </div>
      )}
    </>
  )
}

function NewPlaylist({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const create = useMutation({
    mutationFn: () => createPlaylist(name.trim()),
    onSuccess: (p) => {
      playlistChanged(qc, p)
      onDone()
      void navigate({ to: '/library/$playlistId', params: { playlistId: p.id } })
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
      <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Name it" aria-label="New playlist's name" maxLength={100} />
      <Button type="submit" disabled={!name.trim() || create.isPending}>
        Create
      </Button>
    </motion.form>
  )
}

function Shelf({ title, lists }: { title?: string; lists: SavedPlaylistSummary[] }) {
  return (
    <section>
      {title && <h2 className="mb-3 px-1 text-headline">{title}</h2>}
      <motion.ul variants={stagger} initial="hidden" animate="show" className="grid grid-cols-2 gap-x-4 gap-y-5 sm:grid-cols-3">
        {lists.map((p) => (
          <motion.li key={p.id} variants={fadeUp}>
            <PlaylistCard playlist={p} />
          </motion.li>
        ))}
      </motion.ul>
    </section>
  )
}

function PlaylistCard({ playlist: p }: { playlist: SavedPlaylistSummary }) {
  const me = useMe()
  const rooms = useQuery(roomsQuery)
  const users = useQuery(usersQuery)
  const room = p.roomId ? rooms.data?.find((r) => r.id === p.roomId) : undefined
  const owner = p.ownerId === me.id ? undefined : users.data?.find((u) => u.id === p.ownerId)
  return (
    <Link
      to="/library/$playlistId"
      params={{ playlistId: p.id }}
      className="group flex flex-col gap-2 rounded-3xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <PlaylistCover playlistId={p.id} songs={p.covers} size={400} className="w-full rounded-3xl transition-transform group-hover:scale-[1.02]" />
      <div className="min-w-0 px-1">
        <p className="truncate font-medium">{p.name}</p>
        <p className="flex items-center gap-1 truncate text-caption text-muted-foreground">
          {p.night ? <Moon className="size-3 shrink-0" /> : room && <Users className="size-3 shrink-0" />}
          <span className="truncate">
            {p.songCount} song{p.songCount === 1 ? '' : 's'}
            {owner ? ` · ${owner.displayName}` : room ? ` · ${room.name}` : ''}
          </span>
        </p>
      </div>
    </Link>
  )
}
