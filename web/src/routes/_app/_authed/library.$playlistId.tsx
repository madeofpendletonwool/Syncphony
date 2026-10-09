import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { ArrowDown, ArrowUp, Check, ListPlus, Moon, Pencil, Shuffle, Trash2, X } from 'lucide-react'
import { motion } from 'motion/react'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { AddButton } from '@/components/add-button'
import { Artwork } from '@/components/artwork'
import { BackButton } from '@/components/back-button'
import { ConfirmButton } from '@/components/confirm-button'
import { Notice } from '@/components/notice'
import { PlaylistCover } from '@/components/playlists/playlist-cover'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { UserAvatar } from '@/components/user-avatar'
import { useAddToLane } from '@/hooks/use-add-to-lane'
import { useMe } from '@/lib/auth'
import { pickRandom, trackKey } from '@/lib/browse'
import { sessionName } from '@/lib/history'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo, fadeUp, stagger } from '@/lib/motion'
import { formatDuration } from '@/lib/now-playing'
import {
  canQueue,
  deletePlaylist,
  laneTrackOfSong,
  moveSong,
  playlistChanged,
  playlistLength,
  removeSong,
  savedPlaylistQuery,
  savedPlaylistsQuery,
  songArtworkUrl,
  updatePlaylist,
  type PlaylistSong,
  type SavedPlaylist,
} from '@/lib/playlists'
import { roomsQuery } from '@/lib/room'
import { usableLinksQuery } from '@/lib/services'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

export const Route = createFileRoute('/_app/_authed/library/$playlistId')({
  loader: ({ context, params }) => context.queryClient.prefetchQuery(savedPlaylistQuery(params.playlistId)),
  component: PlaylistPage,
})

// The server queues at most 100 songs per request.
const MAX_ADD = 100
// How many songs shuffling a big playlist adds to your lane.
const SHUFFLE_COUNT = 20

/** One Syncphony playlist: queue it, or (if it's yours) edit it. */
function PlaylistPage() {
  const { playlistId } = Route.useParams()
  const me = useMe()
  const playlist = useQuery(savedPlaylistQuery(playlistId))
  const links = useQuery(usableLinksQuery)
  const { add, status, room } = useAddToLane()
  const [editing, setEditing] = useState(false)

  if (playlist.isPending) {
    return (
      <>
        <BackButton fallback="/library" />
        <div className="flex flex-col items-center gap-4 pt-4">
          <Skeleton className="size-56 rounded-3xl" />
          <Skeleton className="h-7 w-48" />
        </div>
      </>
    )
  }
  if (playlist.isError) {
    return (
      <>
        <BackButton fallback="/library" />
        <Notice className="mt-6">{errorMessage(playlist.error)}</Notice>
      </>
    )
  }

  const p = playlist.data
  const editable = p.ownerId === me.id || me.role === 'admin'
  const usable = new Set((links.data ?? []).filter((l) => l.status === 'ok').map((l) => l.id))
  const borrow = !!room?.matching.borrow
  const queueable = p.songs.filter((s) => canQueue(s, usable, borrow))
  // A song can be in a playlist twice; it's queued once.
  const seen = new Set<string>()
  const waiting = queueable
    .map((s) => laneTrackOfSong(s, s.track.linkId!))
    .filter((t) => {
      const k = trackKey(t)
      if (seen.has(k) || status(t) !== 'idle') return false
      seen.add(k)
      return true
    })
  const cantQueue = p.songs.length - queueable.length

  return (
    <>
      <BackButton fallback="/library" />
      <motion.header variants={stagger} initial="hidden" animate="show" className="flex flex-col items-center pt-2 pb-6 text-center">
        <motion.div initial={{ opacity: 0, scale: 0.92 }} animate={{ opacity: 1, scale: 1 }} transition={{ duration: 0.6, ease: easeOutExpo }}>
          <PlaylistCover playlistId={p.id} songs={p.songs} size={600} className="size-56 rounded-3xl sm:size-64" />
        </motion.div>
        {editing ? <Rename playlist={p} /> : <motion.h1 variants={fadeUp} className="mt-6 text-title text-balance">{p.name}</motion.h1>}
        <motion.p variants={fadeUp} className="mt-1 text-caption text-muted-foreground">
          {playlistLength(p.songs)}
        </motion.p>
        <Byline playlist={p} />
        {!editing && (
          <motion.div variants={fadeUp} className="mt-5 flex flex-wrap justify-center gap-2">
            <Button size="lg" disabled={waiting.length === 0 || !room} onClick={() => add(waiting.slice(0, MAX_ADD))}>
              <ListPlus data-icon="inline-start" />
              {!room
                ? 'Join a room to play it'
                : waiting.length === 0
                  ? queueable.length === 0
                    ? 'Nothing to add'
                    : 'All in your lane'
                  : waiting.length > MAX_ADD
                    ? `Add the next ${MAX_ADD} to my lane`
                    : waiting.length === p.songs.length
                      ? 'Add to my lane'
                      : `Add ${waiting.length} to my lane`}
            </Button>
            {/* Your lane takes turns with everyone else's, so a big add stays fair. */}
            {waiting.length > SHUFFLE_COUNT && room && (
              <Button size="lg" variant="outline" onClick={() => add(pickRandom(waiting, SHUFFLE_COUNT))}>
                <Shuffle data-icon="inline-start" />
                Shuffle in {SHUFFLE_COUNT}
              </Button>
            )}
          </motion.div>
        )}
        {editable && (
          <motion.div variants={fadeUp} className="mt-3">
            <Button variant="ghost" size="sm" onClick={() => setEditing((e) => !e)}>
              {editing ? <Check data-icon="inline-start" /> : <Pencil data-icon="inline-start" />}
              {editing ? 'Done' : 'Edit'}
            </Button>
          </motion.div>
        )}
      </motion.header>

      {editing && <Settings playlist={p} />}
      {cantQueue > 0 && !editing && room && (
        <p className="mb-3 px-1 text-sm text-muted-foreground">
          {cantQueue === 1 ? '1 song is' : `${cantQueue} songs are`} on someone else&apos;s service, and this room doesn&apos;t let people borrow songs.
        </p>
      )}

      {p.songs.length === 0 ? (
        <p className="mt-6 text-center text-sm text-muted-foreground">No songs yet. Save some with the bookmark next to a song.</p>
      ) : (
        <motion.ol variants={stagger} initial="hidden" animate="show" className="glass flex flex-col rounded-3xl px-2 py-2">
          {p.songs.map((s, i) => (
            <SongRow
              key={s.id}
              playlist={p}
              song={s}
              index={i}
              editing={editing}
              add={
                room && canQueue(s, usable, borrow)
                  ? (() => {
                      const t = laneTrackOfSong(s, s.track.linkId!)
                      return { status: status(t), onAdd: () => add([t]) }
                    })()
                  : undefined
              }
            />
          ))}
        </motion.ol>
      )}
    </>
  )
}

/** Who made it, where it's shared, and the night it came from. */
function Byline({ playlist: p }: { playlist: SavedPlaylist }) {
  const me = useMe()
  const users = useQuery(usersQuery)
  const rooms = useQuery(roomsQuery)
  const owner = users.data?.find((u) => u.id === p.ownerId)
  const room = p.roomId ? rooms.data?.find((r) => r.id === p.roomId) : undefined
  const parts = [
    p.night && `Saved from ${sessionName({ startedAt: p.night.from })}, ${new Date(p.night.from).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}`,
    room && `Shared with ${room.name}`,
  ].filter(Boolean)
  return (
    <motion.div variants={fadeUp} className="mt-2 flex flex-wrap items-center justify-center gap-x-2 gap-y-1 text-caption text-muted-foreground">
      {owner && p.ownerId !== me.id && (
        <span className="flex items-center gap-1.5">
          <UserAvatar user={owner} className="size-5 text-[0.55rem]" />
          {owner.displayName}
        </span>
      )}
      {parts.length > 0 && (
        <span className="flex items-center gap-1">
          {p.night && <Moon className="size-3" />}
          {parts.join(' · ')}
        </span>
      )}
    </motion.div>
  )
}

function Rename({ playlist: p }: { playlist: SavedPlaylist }) {
  const qc = useQueryClient()
  const [name, setName] = useState(p.name)
  const rename = useMutation({
    mutationFn: () => updatePlaylist(p.id, { name: name.trim() }),
    onSuccess: (next) => {
      playlistChanged(qc, next)
      toast({ message: 'Renamed' })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  return (
    <form
      className="mt-6 flex w-full max-w-sm gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        if (name.trim() && name.trim() !== p.name) rename.mutate()
      }}
    >
      <Input value={name} onChange={(e) => setName(e.target.value)} aria-label="Playlist name" maxLength={100} />
      <Button type="submit" variant="secondary" disabled={!name.trim() || name.trim() === p.name || rename.isPending}>
        Rename
      </Button>
    </form>
  )
}

/** Sharing with a room, and deleting it. */
function Settings({ playlist: p }: { playlist: SavedPlaylist }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const rooms = useQuery(roomsQuery)
  const { room: current } = useAddToLane()
  // Share with the room it's shared with, else the room you're in.
  const target = rooms.data?.find((r) => r.id === p.roomId) ?? current
  const share = useMutation({
    mutationFn: (on: boolean) => updatePlaylist(p.id, { roomId: on && target ? target.id : '' }),
    onSuccess: (next) => playlistChanged(qc, next),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const remove = useMutation({
    mutationFn: () => deletePlaylist(p.id),
    onSuccess: () => {
      qc.removeQueries({ queryKey: savedPlaylistQuery(p.id).queryKey })
      void qc.invalidateQueries({ queryKey: savedPlaylistsQuery.queryKey })
      toast({ message: `Deleted ${p.name}` })
      void navigate({ to: '/library' })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  return (
    <section className="glass mb-4 flex flex-col gap-3 rounded-3xl p-4">
      {target && (
        <label className="flex items-center justify-between gap-3">
          <span className="min-w-0">
            <span className="block font-medium">Share with {target.name}</span>
            <span className="text-sm text-muted-foreground">Everyone in the room can see it and play from it. Only you can change it.</span>
          </span>
          <Switch checked={!!p.roomId} onChange={(on) => share.mutate(on)} label={`Share with ${target.name}`} />
        </label>
      )}
      <ConfirmButton
        label="Delete playlist"
        confirmLabel="Delete it for good"
        icon={<Trash2 />}
        pending={remove.isPending}
        onConfirm={() => remove.mutate()}
        className="self-start"
      />
    </section>
  )
}

function SongRow({
  playlist,
  song: s,
  index,
  editing,
  add,
}: {
  playlist: SavedPlaylist
  song: PlaylistSong
  index: number
  editing: boolean
  add?: { status: ReturnType<ReturnType<typeof useAddToLane>['status']>; onAdd: () => void }
}) {
  const qc = useQueryClient()
  const users = useQuery(usersQuery)
  const by = s.addedBy ? users.data?.find((u) => u.id === s.addedBy) : undefined
  const change = useMutation({
    mutationFn: (op: { move: number } | 'remove') => (op === 'remove' ? removeSong(playlist.id, s.id) : moveSong(playlist.id, s.id, op.move)),
    onSuccess: (next) => playlistChanged(qc, next),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const last = index === playlist.songs.length - 1
  return (
    <motion.li
      layout="position"
      variants={fadeUp}
      style={laneStyle(by?.color)}
      className="relative flex items-center gap-3 rounded-2xl py-1.5 pr-1 pl-3 before:absolute before:inset-y-3 before:left-0 before:w-1 before:rounded-full before:bg-(--lane)"
    >
      <Artwork src={songArtworkUrl(playlist.id, s, 120)} className="size-12 rounded-lg shadow-none" />
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium">{s.track.title}</p>
        <p className="flex items-center gap-1.5 truncate text-sm text-muted-foreground">
          {by && <UserAvatar user={by} className="size-4 text-[0.5rem]" />}
          <span className="truncate">{s.track.artists.join(', ')}</span>
        </p>
      </div>
      {editing ? (
        <div className={cn('flex shrink-0 items-center', change.isPending && 'opacity-50')}>
          <Button size="icon-sm" variant="ghost" aria-label={`Move ${s.track.title} up`} disabled={index === 0 || change.isPending} onClick={() => change.mutate({ move: index - 1 })}>
            <ArrowUp />
          </Button>
          <Button size="icon-sm" variant="ghost" aria-label={`Move ${s.track.title} down`} disabled={last || change.isPending} onClick={() => change.mutate({ move: index + 1 })}>
            <ArrowDown />
          </Button>
          <Button size="icon-sm" variant="ghost" aria-label={`Remove ${s.track.title}`} disabled={change.isPending} onClick={() => change.mutate('remove')}>
            <X />
          </Button>
        </div>
      ) : (
        <>
          <span className="hidden shrink-0 text-sm text-muted-foreground tabular-nums sm:block">{formatDuration(s.track.durationMs)}</span>
          {add && <AddButton status={add.status} onAdd={add.onAdd} title={s.track.title} />}
        </>
      )}
    </motion.li>
  )
}
