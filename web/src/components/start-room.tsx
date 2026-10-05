import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Disc3, LoaderCircle } from 'lucide-react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { useMe } from '@/lib/auth'
import { chooseRoom, roomsQuery, useCurrentRoom } from '@/lib/room'
import { cn } from '@/lib/utils'

/** Songs go into a room; offers to start one when the server has none. */
export function StartRoom({ className }: { className?: string }) {
  const me = useMe()
  const { room, rooms } = useCurrentRoom()
  const queryClient = useQueryClient()
  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/rooms', { body: { name: `${me.displayName.split(' ')[0]}'s room` } })),
    onSuccess: (r) => {
      queryClient.setQueryData(roomsQuery.queryKey, (rs = []) => [...rs, r])
      chooseRoom(r.id)
    },
  })
  if (!rooms.isSuccess || room) return null
  return (
    <section className={cn('glass flex items-center gap-3 rounded-3xl p-4', className)}>
      <div className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary/15 text-primary">
        <Disc3 className="size-5" />
      </div>
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">No room yet</p>
        <p className="text-caption text-muted-foreground">
          {create.error ? errorMessage(create.error) : 'Start one so everyone can add songs.'}
        </p>
      </div>
      <Button size="sm" onClick={() => create.mutate()} disabled={create.isPending}>
        {create.isPending && <LoaderCircle className="animate-spin" />}
        Start a room
      </Button>
    </section>
  )
}
