import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { KeyRound, LoaderCircle, TicketX } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState, type FormEvent } from 'react'
import { api } from '@/api/client'
import { ApiError, errorMessage, unwrap } from '@/api/errors'
import { AuthScreen, OrDivider } from '@/components/auth-screen'
import { Field } from '@/components/field'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { meQuery, type Me } from '@/lib/auth'
import { easeOutExpo } from '@/lib/motion'
import { relativeTime } from '@/lib/time'
import { suggestUsername, usernameProblem } from '@/lib/username'
import { createPasskey, deviceName, PasskeyCancelled, passkeysSupported } from '@/lib/webauthn'

// Invite links are `${SYNCPHONY_BASE_URL}/invite/<code>` (server/internal/auth).
export const Route = createFileRoute('/invite/$code')({
  component: Invite,
})

function Invite() {
  const { code } = Route.useParams()
  const invite = useQuery({
    queryKey: ['invite', code],
    queryFn: () => unwrap(api.GET('/invites/{code}', { params: { path: { code } } })),
  })

  if (invite.isPending) {
    return (
      <AuthScreen title="You're invited" subtitle="Checking your invite…">
        <div className="glass flex flex-col gap-4 rounded-3xl p-5">
          <Skeleton className="h-11 rounded-xl" />
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
        title={gone ? 'This invite has expired' : "Couldn't check this invite"}
        subtitle={gone ? 'It was already used, revoked, or ran out of time. Ask for a new link.' : errorMessage(invite.error)}
      >
        <div className="flex flex-col items-center gap-3">
          <div className="grid size-14 place-items-center rounded-2xl bg-muted text-muted-foreground">
            <TicketX className="size-7" />
          </div>
          {gone ? (
            <Button asChild variant="glass" size="lg" className="mt-4 w-full">
              <Link to="/login">I already have an account</Link>
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

  const setup = invite.data.role === 'admin'
  return (
    <AuthScreen
      title={setup ? 'Set up Syncphony' : "You're invited"}
      subtitle={
        setup
          ? "You'll be the admin: you can invite friends and manage the server."
          : 'Pick a name. Everyone sees it next to the songs you add.'
      }
    >
      <SignupForm code={code} expiresAt={invite.data.expiresAt} />
    </AuthScreen>
  )
}

function SignupForm({ code, expiresAt }: { code: string; expiresAt: string }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const current = useQuery({ ...meQuery, retry: false }).data
  const canPasskey = passkeysSupported()
  const [usePassword, setUsePassword] = useState(!canPasskey)
  const [displayName, setDisplayName] = useState('')
  // Follows the display name until the user edits it.
  const [username, setUsername] = useState<string>()
  const [password, setPassword] = useState('')
  const [touched, setTouched] = useState(false)

  const effectiveUsername = username ?? suggestUsername(displayName)
  const usernameError = (touched || username !== undefined) && effectiveUsername ? usernameProblem(effectiveUsername) : undefined

  const signedUp = (me: Me) => {
    queryClient.removeQueries()
    queryClient.setQueryData(meQuery.queryKey, me)
    return navigate({ to: '/settings/services', search: { welcome: true }, replace: true })
  }

  const signup = useMutation({
    mutationFn: async (): Promise<Me> => {
      const profile = { invite: code, username: effectiveUsername, displayName: displayName.trim() }
      if (usePassword) return unwrap(api.POST('/auth/signup', { body: { ...profile, password } }))
      const ceremony = await unwrap(api.POST('/auth/signup/passkey/begin', { body: profile }))
      const credential = await createPasskey(ceremony.options)
      return unwrap(
        api.POST('/auth/signup/passkey/finish', {
          body: { ceremonyId: ceremony.ceremonyId, credential, name: deviceName() },
        }),
      )
    },
    onSuccess: signedUp,
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    setTouched(true)
    if (!displayName.trim() || usernameProblem(effectiveUsername)) return
    signup.mutate()
  }

  const err = signup.error instanceof PasskeyCancelled ? null : signup.error
  const taken = err instanceof ApiError && err.code === 'username_taken'

  return (
    <form onSubmit={submit} noValidate className="glass flex flex-col gap-4 rounded-3xl p-5">
      <Notice tone="info">{current && <>You&apos;re signed in as {current.displayName}. Signing up switches to the new account.</>}</Notice>
      <Notice>{err && !taken && errorMessage(err)}</Notice>

      <Field
        label="Name"
        name="name"
        autoComplete="nickname"
        autoFocus
        required
        maxLength={64}
        placeholder="What friends call you"
        value={displayName}
        error={touched && !displayName.trim() ? 'Add a name.' : undefined}
        onChange={(e) => setDisplayName(e.target.value)}
      />
      <Field
        label="Username"
        name="username"
        autoComplete="username"
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        required
        maxLength={32}
        value={effectiveUsername}
        error={taken ? errorMessage(err) : usernameError}
        help="For signing in. Lowercase letters, numbers, and . _ -"
        onChange={(e) => {
          setUsername(e.target.value.toLowerCase())
          if (taken) signup.reset()
        }}
      />

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
              label="Password"
              name="password"
              secret
              autoComplete="new-password"
              required
              minLength={8}
              maxLength={256}
              value={password}
              help="At least 8 characters."
              error={touched && password.length < 8 ? 'At least 8 characters.' : undefined}
              onChange={(e) => setPassword(e.target.value)}
            />
          </motion.div>
        )}
      </AnimatePresence>

      <Button
        type="submit"
        size="lg"
        className="mt-1"
        disabled={signup.isPending || (usePassword && touched && password.length < 8)}
      >
        {signup.isPending ? (
          <LoaderCircle className="animate-spin" />
        ) : (
          !usePassword && <KeyRound data-icon="inline-start" />
        )}
        {usePassword ? 'Create account' : 'Create account with a passkey'}
      </Button>

      {canPasskey && (
        <>
          <OrDivider />
          <Button
            type="button"
            variant="ghost"
            className="-mt-1"
            onClick={() => {
              signup.reset()
              setUsePassword((p) => !p)
            }}
          >
            {usePassword ? 'Use a passkey instead' : 'Use a password instead'}
          </Button>
        </>
      )}

      <p className="text-center text-caption text-muted-foreground">
        Invite expires {relativeTime(expiresAt)}.{' '}
        <Link to="/login" className="underline-offset-4 hover:text-foreground hover:underline">
          Already have an account?
        </Link>
      </p>
    </form>
  )
}
