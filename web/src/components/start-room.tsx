import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ChevronRight, Disc3, LoaderCircle, Plus } from 'lucide-react'
import { motion } from 'motion/react'
import { useState } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { UserAvatar } from '@/components/user-avatar'
import { useMe } from '@/lib/auth'
import { fadeUp, stagger } from '@/lib/motion'
import { chooseRoom, roomsQuery, useCurrentRoom } from '@/lib/room'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

/** Every room on the server, to join one, and a form to start a new one. */
export function RoomLobby() {
  const { rooms } = useCurrentRoom()
  const users = useQuery(usersQuery)
  const list = rooms.data ?? []

  return (
    <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-6">
      {list.length > 0 && (
        <motion.ul variants={fadeUp} className="glass flex flex-col rounded-3xl p-1.5">
          {list.map((r) => {
            const owner = users.data?.find((u) => u.id === r.ownerId)
            return (
              <li key={r.id}>
                <button
                  type="button"
                  onClick={() => chooseRoom(r.id)}
                  aria-label={owner ? `Join ${r.name}, started by ${owner.displayName}` : `Join ${r.name}`}
                  className="flex w-full items-center gap-3 rounded-2xl p-2.5 text-left transition-colors outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  <span className="grid size-11 shrink-0 place-items-center rounded-xl bg-primary/15 text-primary">
                    <Disc3 className="size-5" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{r.name}</span>
                    <span className="flex items-center gap-1.5 text-sm text-muted-foreground">
                      {owner && <UserAvatar user={owner} className="size-4 text-[0.5rem]" />}
                      {owner ? `Started by ${owner.displayName}` : 'Room'}
                    </span>
                  </span>
                  <span className="flex items-center gap-0.5 text-sm font-medium text-primary">
                    Join
                    <ChevronRight className="size-4" />
                  </span>
                </button>
              </li>
            )
          })}
        </motion.ul>
      )}
      <motion.div variants={fadeUp}>
        <NewRoomForm first={list.length === 0} />
      </motion.div>
    </motion.div>
  )
}

function NewRoomForm({ first }: { first: boolean }) {
  const me = useMe()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const fallback = `${me.displayName.split(' ')[0]}'s room`
  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/rooms', { body: { name: name.trim() || fallback } })),
    onSuccess: (r) => {
      queryClient.setQueryData(roomsQuery.queryKey, (rs = []) => [...rs, r])
      chooseRoom(r.id)
    },
  })

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        create.mutate()
      }}
      className="glass flex flex-col gap-3 rounded-3xl p-4"
    >
      <div>
        <p className="font-medium">{first ? 'Start the first room' : 'Start a new room'}</p>
        <p className={cn('text-caption', create.error ? 'text-destructive' : 'text-muted-foreground')}>
          {create.error ? errorMessage(create.error) : 'Everyone on this server can join it.'}
        </p>
      </div>
      <div className="flex gap-2">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={fallback}
          maxLength={64}
          aria-label="Room name"
        />
        <Button type="submit" disabled={create.isPending} className="h-11 shrink-0">
          {create.isPending ? <LoaderCircle className="animate-spin" /> : <Plus data-icon="inline-start" />}
          Start
        </Button>
      </div>
    </form>
  )
}

/** Songs go into a room; points you at the room list when you're not in one. */
export function JoinRoomPrompt({ className }: { className?: string }) {
  const { room, rooms } = useCurrentRoom()
  if (!rooms.isSuccess || room) return null
  return (
    <section className={cn('glass flex items-center gap-3 rounded-3xl p-4', className)}>
      <div className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary/15 text-primary">
        <Disc3 className="size-5" />
      </div>
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">You&apos;re not in a room</p>
        <p className="text-caption text-muted-foreground">Join one so you can add songs.</p>
      </div>
      <Button asChild size="sm">
        <Link to="/room">{rooms.data.length > 0 ? 'Pick a room' : 'Start a room'}</Link>
      </Button>
    </section>
  )
}
