import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Disc3, LoaderCircle, TicketX } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { ApiError, errorMessage } from '@/api/errors'
import { AuthScreen } from '@/components/auth-screen'
import { Field } from '@/components/field'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { meQuery, rememberMe } from '@/lib/auth'
import { guestInviteQuery, isGuest, joinAsGuest, type GuestInvite } from '@/lib/guests'
import { chooseRoom } from '@/lib/room'
import { relativeTime } from '@/lib/time'

// Guest passes (MAD-720) are `${SYNCPHONY_BASE_URL}/join/<signed pass>`,
// shown as a QR code by someone in the room or the big screen.
export const Route = createFileRoute('/join/$token')({
  component: Join,
})

function Join() {
  const { token } = Route.useParams()
  const invite = useQuery(guestInviteQuery(token))

  if (invite.isPending) {
    return (
      <AuthScreen title="Join the room" subtitle="Checking your pass…">
        <div className="glass flex flex-col gap-4 rounded-3xl p-5">
          <Skeleton className="h-11 rounded-xl" />
          <Skeleton className="h-12 rounded-full" />
        </div>
      </AuthScreen>
    )
  }

  if (invite.isError) {
    const gone = invite.error instanceof ApiError && invite.error.status === 404
    return (
      <AuthScreen
        title={gone ? 'This pass has expired' : "Couldn't check this pass"}
        subtitle={gone ? 'Ask someone in the room to show the code again.' : errorMessage(invite.error)}
      >
        <div className="flex flex-col items-center gap-3">
          <div className="grid size-14 place-items-center rounded-2xl bg-muted text-muted-foreground">
            <TicketX className="size-7" />
          </div>
          {gone ? (
            <Button asChild variant="glass" size="lg" className="mt-4 w-full">
              <Link to="/login">I have an account</Link>
            </Button>
          ) : (
            <Button variant="glass" size="lg" className="mt-4 w-full" onClick={() => invite.refetch()}>
              Try again
            </Button>
          )}
        </div>
      </AuthScreen>
    )
  }

  return (
    <AuthScreen title={`Join ${invite.data.roomName}`} subtitle="Add songs to the queue. Everyone takes turns, you included.">
      <JoinForm token={token} invite={invite.data} />
    </AuthScreen>
  )
}

function JoinForm({ token, invite }: { token: string; invite: GuestInvite }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const current = useQuery({ ...meQuery, retry: false }).data
  const [name, setName] = useState('')
  const [touched, setTouched] = useState(false)

  const enter = () => {
    chooseRoom(invite.roomId)
    return navigate({ to: '/room', replace: true })
  }
  const join = useMutation({
    mutationFn: () => joinAsGuest(token, name.trim()),
    onSuccess: (me) => {
      queryClient.removeQueries()
      rememberMe(me)
      queryClient.setQueryData(meQuery.queryKey, me)
      return enter()
    },
  })

  // Members (and guests already in this room) just go in.
  const member = current && !isGuest(current)
  const here = current?.guest?.roomId === invite.roomId && !current.guest.ended
  if (member || here) {
    return (
      <div className="glass flex flex-col gap-4 rounded-3xl p-5">
        <p className="text-sm text-muted-foreground">
          You&apos;re signed in as <span className="font-medium text-foreground">{current.displayName}</span>.
        </p>
        <Button size="lg" onClick={() => void enter()}>
          <Disc3 data-icon="inline-start" />
          Open {invite.roomName}
        </Button>
      </div>
    )
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setTouched(true)
    if (name.trim()) join.mutate()
  }

  return (
    <form onSubmit={submit} noValidate className="glass flex flex-col gap-4 rounded-3xl p-5">
      <Notice>{join.error && errorMessage(join.error)}</Notice>
      <Field
        label="Your name"
        name="name"
        autoComplete="nickname"
        autoFocus
        required
        maxLength={32}
        placeholder="So everyone knows who picked what"
        value={name}
        error={touched && !name.trim() ? 'Add a name.' : undefined}
        onChange={(e) => setName(e.target.value)}
      />
      <Button type="submit" size="lg" className="mt-1" disabled={join.isPending}>
        {join.isPending ? <LoaderCircle className="animate-spin" /> : <Disc3 data-icon="inline-start" />}
        Join as a guest
      </Button>
      <p className="text-center text-caption text-muted-foreground">
        No account needed. Your pass ends {relativeTime(invite.expiresAt)}.{' '}
        <Link to="/login" className="underline-offset-4 hover:text-foreground hover:underline">
          Have an account?
        </Link>
      </p>
    </form>
  )
}
