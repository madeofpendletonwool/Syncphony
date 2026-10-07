import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Clock, Disc3, LoaderCircle, TicketX } from 'lucide-react'
import { ApiError, errorMessage } from '@/api/errors'
import { AuthScreen } from '@/components/auth-screen'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { redeemInvite, removeMember, roomInviteQuery, visibility, type RoomInvitePreview } from '@/lib/access'
import { meQuery } from '@/lib/auth'
import { isGuest } from '@/lib/guests'
import { chooseRoom, roomsQuery } from '@/lib/room'
import { usersQuery } from '@/lib/users'

// Room invites (ADR 0011) are `${SYNCPHONY_BASE_URL}/room-invite/<code>`:
// a link into an unlisted or private room, for members of the server.
export const Route = createFileRoute('/room-invite/$code')({
  component: RoomInvite,
})

function RoomInvite() {
  const { code } = Route.useParams()
  const me = useQuery({ ...meQuery, retry: false })
  const signedIn = !!me.data && !isGuest(me.data)
  const invite = useQuery({ ...roomInviteQuery(code), enabled: signedIn })

  if (me.isPending) return <Checking />
  if (!signedIn) {
    return (
      <AuthScreen title="You're invited to a room" subtitle="Sign in to this Syncphony server to join it.">
        <Button asChild size="lg" className="w-full">
          <Link to="/login" search={{ redirect: `/room-invite/${code}` }}>
            Sign in
          </Link>
        </Button>
        {me.data && <p className="mt-3 text-center text-caption text-muted-foreground">Guests can only be in the room their pass is for.</p>}
      </AuthScreen>
    )
  }
  if (invite.isPending) return <Checking />
  if (invite.isError) {
    const gone = invite.error instanceof ApiError && invite.error.status === 404
    return (
      <AuthScreen
        title={gone ? 'This link has expired' : "Couldn't check this link"}
        subtitle={gone ? 'Ask someone in the room for a new one.' : errorMessage(invite.error)}
      >
        <div className="flex flex-col items-center gap-3">
          <div className="grid size-14 place-items-center rounded-2xl bg-muted text-muted-foreground">
            <TicketX className="size-7" />
          </div>
          <Button asChild variant="glass" size="lg" className="mt-4 w-full">
            <Link to="/room">Go to your rooms</Link>
          </Button>
        </div>
      </AuthScreen>
    )
  }
  return <Join code={code} invite={invite.data} />
}

function Checking() {
  return (
    <AuthScreen title="Join the room" subtitle="Checking your link…">
      <div className="glass flex flex-col gap-4 rounded-3xl p-5">
        <Skeleton className="h-11 rounded-xl" />
        <Skeleton className="h-12 rounded-full" />
      </div>
    </AuthScreen>
  )
}

function Join({ code, invite }: { code: string; invite: RoomInvitePreview }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const users = useQuery(usersQuery)
  const owner = users.data?.find((u) => u.id === invite.ownerId)
  const v = visibility(invite.visibility)

  const enter = async () => {
    await queryClient.invalidateQueries({ queryKey: roomsQuery.queryKey })
    chooseRoom(invite.roomId)
    return navigate({ to: '/room', replace: true })
  }
  const join = useMutation({
    mutationFn: () => redeemInvite(code),
    onSuccess: (res) => {
      queryClient.setQueryData(roomInviteQuery(code).queryKey, res.room)
      if (res.status === 'member') return enter()
    },
  })
  const cancel = useMutation({
    mutationFn: async () => {
      const me = queryClient.getQueryData(meQuery.queryKey)
      if (me) await removeMember(invite.roomId, me.id)
    },
    onSuccess: () => queryClient.setQueryData(roomInviteQuery(code).queryKey, { ...invite, status: 'none' as const }),
  })

  if (invite.status === 'pending') {
    return (
      <AuthScreen title={`You asked to join ${invite.roomName}`} subtitle={`${owner?.displayName ?? 'Its owner'} will let you in. It shows up in your rooms once they do.`}>
        <div className="glass flex flex-col items-center gap-4 rounded-3xl p-5">
          <div className="grid size-12 place-items-center rounded-2xl bg-primary/15 text-primary">
            <Clock className="size-6" />
          </div>
          <Notice>{cancel.error && errorMessage(cancel.error)}</Notice>
          <div className="flex w-full flex-col gap-2">
            <Button asChild size="lg">
              <Link to="/room">Back to your rooms</Link>
            </Button>
            <Button variant="ghost" disabled={cancel.isPending} onClick={() => cancel.mutate()}>
              Take back my request
            </Button>
          </div>
        </div>
      </AuthScreen>
    )
  }

  return (
    <AuthScreen
      title={invite.status === 'member' ? `You're in ${invite.roomName}` : `Join ${invite.roomName}`}
      subtitle={
        <span className="inline-flex items-center gap-1.5">
          <v.icon className="size-4" />
          {v.label} room{owner ? ` · ${owner.displayName}'s` : ''}
        </span>
      }
    >
      <div className="glass flex flex-col gap-4 rounded-3xl p-5">
        <Notice>{join.error && errorMessage(join.error)}</Notice>
        {invite.status === 'member' ? (
          <Button size="lg" onClick={() => void enter()}>
            <Disc3 data-icon="inline-start" />
            Open {invite.roomName}
          </Button>
        ) : (
          <>
            <Button size="lg" disabled={join.isPending} onClick={() => join.mutate()}>
              {join.isPending ? <LoaderCircle className="animate-spin" /> : <Disc3 data-icon="inline-start" />}
              {invite.approval ? 'Ask to join' : 'Join the room'}
            </Button>
            <p className="text-center text-caption text-muted-foreground">
              {invite.approval
                ? `${owner?.displayName ?? 'The owner'} lets each person in.`
                : 'Add songs to the queue. Everyone takes turns, you included.'}
            </p>
          </>
        )}
      </div>
    </AuthScreen>
  )
}
