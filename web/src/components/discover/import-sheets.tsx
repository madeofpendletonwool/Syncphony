import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { LoaderCircle } from 'lucide-react'
import { useMemo, useState } from 'react'
import { errorMessage } from '@/api/errors'
import { Notice } from '@/components/notice'
import { Sheet } from '@/components/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  createSet,
  importArtists,
  roomTasteQuery,
  setChanged,
  starredQuery,
  starredText,
  type DiscoverySet,
  type DiscoveryTasteArtist,
} from '@/lib/discover'
import { nightsQuery } from '@/lib/nights'
import { roomsQuery } from '@/lib/room'
import { toast } from '@/lib/toast'
import { PickList } from './pick-list'

const selectClass =
  'h-10 w-full rounded-xl border border-input bg-transparent px-3 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50'

/** Artists you starred on your Navidrome, to pick from: a one-time import, never automatic. */
export function StarredSheet({ set, open, onOpenChange }: { set: DiscoverySet; open: boolean; onOpenChange: (open: boolean) => void }) {
  const qc = useQueryClient()
  const starred = useQuery({ ...starredQuery, enabled: open })
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const have = useMemo(() => new Set(set.artists.map((a) => a.name.toLowerCase())), [set.artists])
  const add = useMutation({
    mutationFn: () => importArtists(set.id, 'starred', [...picked]),
    onSuccess: (s) => {
      setChanged(qc, s)
      toast({ message: `Added ${picked.size} artist${picked.size === 1 ? '' : 's'} to ${s.name}` })
      setPicked(new Set())
      onOpenChange(false)
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title="Add from your Navidrome stars"
      description="Pick the ones that fit this set. Starring something doesn't make it this set's taste, so nothing's added unless you check it."
    >
      {starred.isPending ? (
        <div className="flex flex-col gap-2">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-12 rounded-2xl" />
          ))}
        </div>
      ) : starred.isError ? (
        <Notice>{errorMessage(starred.error)}</Notice>
      ) : starred.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">Nothing starred on a Navidrome of yours. Star artists, albums or songs there and they show up here.</p>
      ) : (
        <PickList
          label="Starred artists"
          picked={picked}
          onChange={setPicked}
          items={starred.data.map((a) => {
            const inSet = have.has(a.name.toLowerCase())
            return { name: a.name, detail: inSet ? 'In this set' : starredText(a), disabled: inSet }
          })}
        />
      )}
      <Button disabled={picked.size === 0 || add.isPending} onClick={() => add.mutate()}>
        {add.isPending && <LoaderCircle data-icon="inline-start" className="animate-spin" />}
        Add {picked.size || ''} to {set.name}
      </Button>
    </Sheet>
  )
}

/** "Bedhead · 4 plays lately", or how much the room likes them. */
function tasteText(a: DiscoveryTasteArtist) {
  if (a.plays > 0) return `${a.plays} play${a.plays === 1 ? '' : 's'}`
  return a.weight >= 0.66 ? 'A favorite' : a.weight >= 0.33 ? 'Liked' : 'Now and then'
}

/** How many of a room's favorites are picked to start with. */
const prePick = 8

/**
 * Makes a set from a room's taste, or a past night's: its favorite
 * artists as a checklist. With `set`, the ones you keep join it;
 * without, they make a new set. It only reads the room.
 */
export function RoomSheet({ set, open, onOpenChange }: { set?: DiscoverySet; open: boolean; onOpenChange: (open: boolean) => void }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const rooms = useQuery({ ...roomsQuery, enabled: open })
  const [chosenRoom, setRoomId] = useState('')
  // The first room, until you pick one.
  const roomId = chosenRoom || rooms.data?.[0]?.id || ''
  const [nightId, setNightId] = useState('')
  const [mine, setMine] = useState(false)
  const [name, setName] = useState('')
  const nights = useQuery({ ...nightsQuery(roomId), enabled: open && !!roomId })
  const taste = useQuery({ ...roomTasteQuery({ roomId, nightId, mine }), enabled: open && !!roomId })
  const have = useMemo(() => new Set(set?.artists.map((a) => a.name.toLowerCase())), [set?.artists])
  // Each taste starts with its favorites picked, until you change them.
  const [choice, setChoice] = useState<{ of: string; picked: Set<string> }>()
  const of = `${roomId}/${nightId}/${mine}`
  const favorites = useMemo(
    () => new Set((taste.data ?? []).filter((a) => !have.has(a.name.toLowerCase())).slice(0, prePick).map((a) => a.name)),
    [taste.data, have],
  )
  const picked = choice?.of === of ? choice.picked : favorites
  const setPicked = (p: Set<string>) => setChoice({ of, picked: p })

  const room = rooms.data?.find((r) => r.id === roomId)
  const night = nights.data?.find((n) => n.id === nightId)
  const defaultName = room ? (night ? `${room.name}, ${new Date(night.endedAt).toLocaleDateString()}` : room.name) : ''

  const add = useMutation({
    mutationFn: async () => {
      const target = set ?? (await createSet(name.trim() || defaultName || 'From a room'))
      return importArtists(target.id, 'room', [...picked])
    },
    onSuccess: (s) => {
      setChanged(qc, s)
      toast({ message: set ? `Added ${picked.size} artist${picked.size === 1 ? '' : 's'} to ${s.name}` : `Made ${s.name}` })
      onOpenChange(false)
      if (!set) void navigate({ to: '/discover/$setId', params: { setId: s.id } })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title={set ? "Add from a room's taste" : 'New set from a room'}
      description="The artists a room likes most, as its DJ reads it. Keep the ones you want. Nothing in the room changes."
    >
      {rooms.data?.length === 0 ? (
        <p className="text-sm text-muted-foreground">You&apos;re not in any rooms yet.</p>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="flex flex-col gap-1 text-caption text-muted-foreground">
            Room
            <select
              className={selectClass}
              value={roomId}
              onChange={(e) => {
                setRoomId(e.target.value)
                setNightId('')
              }}
            >
              {rooms.data?.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-caption text-muted-foreground">
            From
            <select className={selectClass} value={nightId} onChange={(e) => setNightId(e.target.value)}>
              <option value="">All its nights and lately</option>
              {nights.data?.map((n) => (
                <option key={n.id} value={n.id}>
                  The night of {new Date(n.endedAt).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}
                </option>
              ))}
            </select>
          </label>
          <div className="flex items-center justify-between gap-3 sm:col-span-2">
            <span className="text-sm">Only songs I picked</span>
            <Switch checked={mine} onChange={setMine} label="Only songs I picked" />
          </div>
        </div>
      )}
      {!set && (
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={defaultName || "The new set's name"} aria-label="The new set's name" maxLength={100} />
      )}
      {roomId &&
        (taste.isPending ? (
          <div className="flex flex-col gap-2">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-12 rounded-2xl" />
            ))}
          </div>
        ) : taste.isError ? (
          <Notice>{errorMessage(taste.error)}</Notice>
        ) : taste.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {mine ? "Nothing you picked there yet." : "This room hasn't played enough to have a taste yet."}
          </p>
        ) : (
          <PickList
            label="The room's artists"
            picked={picked}
            onChange={setPicked}
            items={taste.data.map((a) => {
              const inSet = have.has(a.name.toLowerCase())
              return { name: a.name, detail: inSet ? 'In this set' : tasteText(a), disabled: inSet }
            })}
          />
        ))}
      <Button disabled={picked.size === 0 || add.isPending} onClick={() => add.mutate()}>
        {add.isPending && <LoaderCircle data-icon="inline-start" className="animate-spin" />}
        {set ? `Add ${picked.size || ''} to ${set.name}` : `Make a set of ${picked.size}`}
      </Button>
    </Sheet>
  )
}
