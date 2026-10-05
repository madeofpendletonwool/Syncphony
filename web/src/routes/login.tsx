import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { KeyRound, LoaderCircle } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState, type FormEvent } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { AuthScreen, OrDivider } from '@/components/auth-screen'
import { Field } from '@/components/field'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { meQuery, safeRedirect, type Me } from '@/lib/auth'
import { easeOutExpo } from '@/lib/motion'
import { getPasskey, PasskeyCancelled, passkeysSupported } from '@/lib/webauthn'

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
  }),
  beforeLoad: async ({ context, search }) => {
    const me = await context.queryClient.ensureQueryData(meQuery).catch(() => null)
    if (me) throw redirect({ href: safeRedirect(search.redirect), replace: true })
  },
  component: Login,
})

function Login() {
  const search = Route.useSearch()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const canPasskey = passkeysSupported()
  const [usePassword, setUsePassword] = useState(!canPasskey)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')

  const signedIn = (me: Me) => {
    queryClient.setQueryData(meQuery.queryKey, me)
    return navigate({ href: safeRedirect(search.redirect), replace: true })
  }

  const passkey = useMutation({
    mutationFn: async () => {
      const ceremony = await unwrap(api.POST('/auth/passkey/begin'))
      const credential = await getPasskey(ceremony.options)
      return unwrap(api.POST('/auth/passkey/finish', { body: { ceremonyId: ceremony.ceremonyId, credential } }))
    },
    onSuccess: signedIn,
  })

  const login = useMutation({
    mutationFn: () => unwrap(api.POST('/auth/login', { body: { username: username.trim(), password } })),
    onSuccess: signedIn,
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    passkey.reset()
    login.mutate()
  }

  const busy = passkey.isPending || login.isPending
  const error = passkey.error instanceof PasskeyCancelled ? null : (passkey.error ?? login.error)

  return (
    <AuthScreen title="Welcome back" subtitle="Sign in to join the queue.">
      <div className="glass flex flex-col rounded-3xl p-5">
        <Notice className="mb-4">{error && errorMessage(error)}</Notice>

        {canPasskey && (
          <Button
            size="lg"
            disabled={busy}
            onClick={() => {
              login.reset()
              passkey.mutate()
            }}
          >
            {passkey.isPending ? <LoaderCircle className="animate-spin" /> : <KeyRound data-icon="inline-start" />}
            Sign in with a passkey
          </Button>
        )}

        {canPasskey && <OrDivider />}

        <AnimatePresence initial={false} mode="popLayout">
          {usePassword ? (
            <motion.form
              key="form"
              onSubmit={submit}
              initial={{ opacity: 0, height: 0 }}
              animate={{ opacity: 1, height: 'auto' }}
              transition={{ duration: 0.35, ease: easeOutExpo }}
              className="flex flex-col gap-4"
            >
              <Field
                label="Username"
                name="username"
                autoComplete="username"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                required
                autoFocus={canPasskey}
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
              <Field
                label="Password"
                name="password"
                secret
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <Button type="submit" size="lg" variant={canPasskey ? 'secondary' : 'default'} disabled={busy}>
                {login.isPending && <LoaderCircle className="animate-spin" />}
                Sign in
              </Button>
            </motion.form>
          ) : (
            <motion.div key="toggle" exit={{ opacity: 0 }}>
              <Button variant="ghost" size="lg" className="w-full" onClick={() => setUsePassword(true)}>
                Use a password instead
              </Button>
            </motion.div>
          )}
        </AnimatePresence>
      </div>

      <p className="mt-6 text-center text-sm text-muted-foreground">
        New here? Ask someone on this server for an invite link.
      </p>
    </AuthScreen>
  )
}
