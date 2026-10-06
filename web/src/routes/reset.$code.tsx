import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { KeyRound, LoaderCircle, TicketX } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState, type FormEvent } from 'react'
import { api } from '@/api/client'
import { ApiError, errorMessage, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { AuthScreen, OrDivider } from '@/components/auth-screen'
import { Field } from '@/components/field'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { UserAvatar } from '@/components/user-avatar'
import { meQuery, rememberMe, type Me } from '@/lib/auth'
import { easeOutExpo } from '@/lib/motion'
import { relativeTime } from '@/lib/time'
import { toast } from '@/lib/toast'
import { createPasskey, deviceName, PasskeyCancelled, passkeysSupported } from '@/lib/webauthn'

type ResetLinkInfo = components['schemas']['ResetLinkInfo']

// Reset links are `${SYNCPHONY_BASE_URL}/reset/<code>` (server/internal/auth).
export const Route = createFileRoute('/reset/$code')({
  component: Reset,
})

function Reset() {
  const { code } = Route.useParams()
  const link = useQuery({
    queryKey: ['reset-link', code],
    queryFn: () => unwrap(api.GET('/reset-links/{code}', { params: { path: { code } } })),
    retry: false,
  })

  if (link.isPending) {
    return (
      <AuthScreen title="Let's get you back in" subtitle="Checking your link…">
        <div className="glass flex flex-col gap-4 rounded-3xl p-5">
          <Skeleton className="h-11 rounded-xl" />
          <Skeleton className="h-12 rounded-full" />
        </div>
      </AuthScreen>
    )
  }

  if (link.isError) {
    const gone = link.error instanceof ApiError && link.error.status === 404
    return (
      <AuthScreen
        title={gone ? 'This link has expired' : "Couldn't check this link"}
        subtitle={gone ? 'It was already used or ran out of time. Ask an admin for a new one.' : errorMessage(link.error)}
      >
        <div className="flex flex-col items-center gap-3">
          <div className="grid size-14 place-items-center rounded-2xl bg-muted text-muted-foreground">
            <TicketX className="size-7" />
          </div>
          {gone ? (
            <Button asChild variant="glass" size="lg" className="mt-4 w-full">
              <Link to="/login">Go to sign in</Link>
            </Button>
          ) : (
            <Button variant="glass" size="lg" className="mt-4 w-full" onClick={() => link.refetch()}>
              Try again
            </Button>
          )}
        </div>
      </AuthScreen>
    )
  }

  return (
    <AuthScreen
      title={`Welcome back, ${link.data.displayName.split(/\s+/)[0]}`}
      subtitle="Set up a new way to sign in. Any device still signed in to your account will be signed out."
    >
      <ResetForm code={code} info={link.data} />
    </AuthScreen>
  )
}

function ResetForm({ code, info }: { code: string; info: ResetLinkInfo }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const canPasskey = passkeysSupported()
  const [usePassword, setUsePassword] = useState(!canPasskey)
  const [password, setPassword] = useState('')
  const [touched, setTouched] = useState(false)
  const path = { params: { path: { code } } }

  const reset = useMutation({
    mutationFn: async (): Promise<Me> => {
      if (usePassword) return unwrap(api.POST('/reset-links/{code}/password', { ...path, body: { newPassword: password } }))
      const ceremony = await unwrap(api.POST('/reset-links/{code}/passkey/begin', path))
      const credential = await createPasskey(ceremony.options)
      return unwrap(
        api.POST('/reset-links/{code}/passkey/finish', {
          ...path,
          body: { ceremonyId: ceremony.ceremonyId, credential, name: deviceName() },
        }),
      )
    },
    onSuccess: (me) => {
      // Whoever was signed in on this device before is gone now.
      queryClient.removeQueries()
      rememberMe(me)
      queryClient.setQueryData(meQuery.queryKey, me)
      toast({ message: "You're back in. Every other device was signed out." }, 6000)
      return navigate({ to: '/settings/security', replace: true })
    },
  })

  const tooShort = [...password].length < 8
  const submit = (e: FormEvent) => {
    e.preventDefault()
    setTouched(true)
    if (usePassword && tooShort) return
    reset.mutate()
  }

  const err = reset.error instanceof PasskeyCancelled ? null : reset.error

  return (
    <form onSubmit={submit} noValidate className="glass flex flex-col gap-4 rounded-3xl p-5">
      <div className="flex items-center gap-3 rounded-2xl bg-muted/50 p-3">
        <UserAvatar user={info} className="size-11" />
        <div className="min-w-0">
          <p className="truncate font-medium">{info.displayName}</p>
          <p className="truncate text-sm text-muted-foreground">@{info.username}</p>
        </div>
      </div>

      <Notice>{err && errorMessage(err)}</Notice>

      {/* Lets password managers save the new password against the right account. */}
      <input type="text" name="username" autoComplete="username" value={info.username} readOnly hidden />
      <AnimatePresence initial={false}>
        {usePassword && (
          <motion.div
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: 'auto' }}
            exit={{ opacity: 0, height: 0 }}
            transition={{ duration: 0.35, ease: easeOutExpo }}
            className="-m-1 overflow-hidden p-1"
          >
            <Field
              label="New password"
              name="password"
              secret
              autoComplete="new-password"
              autoFocus
              required
              minLength={8}
              maxLength={256}
              value={password}
              help="At least 8 characters."
              error={touched && tooShort ? 'At least 8 characters.' : undefined}
              onChange={(e) => setPassword(e.target.value)}
            />
          </motion.div>
        )}
      </AnimatePresence>

      <Button type="submit" size="lg" className="mt-1" disabled={reset.isPending || (usePassword && touched && tooShort)}>
        {reset.isPending ? <LoaderCircle className="animate-spin" /> : !usePassword && <KeyRound data-icon="inline-start" />}
        {usePassword ? 'Set password and sign in' : 'Create a passkey and sign in'}
      </Button>

      {canPasskey && (
        <>
          <OrDivider />
          <Button
            type="button"
            variant="ghost"
            className="-mt-1"
            onClick={() => {
              reset.reset()
              setUsePassword((p) => !p)
            }}
          >
            {usePassword ? 'Use a passkey instead' : 'Set a password instead'}
          </Button>
        </>
      )}

      <p className="text-center text-caption text-muted-foreground">
        This link works once and expires {relativeTime(info.expiresAt)}.
      </p>
    </form>
  )
}
