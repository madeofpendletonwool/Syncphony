import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, redirect } from '@tanstack/react-router'
import {
  Check,
  CircleHelp,
  KeyRound,
  Laptop,
  LoaderCircle,
  LogOut,
  Pencil,
  Plus,
  Smartphone,
  Tablet,
  Trash2,
  X,
} from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState, type FormEvent, type ReactNode } from 'react'
import { api } from '@/api/client'
import { ApiError, errorMessage, unwrap } from '@/api/errors'
import { Field } from '@/components/field'
import { Notice } from '@/components/notice'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmButton } from '@/components/confirm-button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  describeDevice,
  mySessionsQuery,
  passkeysQuery,
  useRefreshMe,
  type DeviceKind,
  type Passkey,
  type SignedInSession,
} from '@/lib/account'
import { meQuery, useMe } from '@/lib/auth'
import { fadeUp, spring, stagger } from '@/lib/motion'
import { relativeTime } from '@/lib/time'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { createPasskey, deviceName, PasskeyCancelled, passkeysSupported } from '@/lib/webauthn'

export const Route = createFileRoute('/_app/_authed/settings/security')({
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.ensureQueryData(meQuery)
    if (me?.guest) throw redirect({ to: '/me', replace: true })
  },
  component: Security,
})

function Security() {
  return (
    <>
      <PageHeader title="Sign-in & security" subtitle="How you sign in, and where you're signed in." />
      <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-4">
        <Password />
        <Passkeys />
        <Devices />
      </motion.div>
    </>
  )
}

function Card({ title, description, children }: { title: string; description: ReactNode; children: ReactNode }) {
  return (
    <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-5">
      <h2 className="text-headline">{title}</h2>
      <p className="mt-1 text-sm text-muted-foreground">{description}</p>
      {children}
    </motion.section>
  )
}

// --- Password -----------------------------------------------------------------

function Password() {
  const me = useMe()
  const queryClient = useQueryClient()
  const refreshMe = useRefreshMe()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [signOutOthers, setSignOutOthers] = useState(true)
  const [done, setDone] = useState<string>()

  const save = useMutation({
    mutationFn: () =>
      unwrap(
        api.PUT('/me/password', {
          body: {
            currentPassword: me.hasPassword ? current : undefined,
            newPassword: next,
            signOutOtherSessions: signOutOthers,
          },
        }),
      ),
    onSuccess: () => {
      setDone(
        me.hasPassword
          ? signOutOthers
            ? 'Password changed. Your other devices were signed out.'
            : 'Password changed.'
          : 'Password set. You can sign in with it on any device.',
      )
      setCurrent('')
      setNext('')
      void refreshMe()
      void queryClient.invalidateQueries({ queryKey: mySessionsQuery.queryKey })
    },
  })

  const remove = useMutation({
    mutationFn: () => unwrap(api.DELETE('/me/password')),
    onSuccess: () => {
      toast({ message: 'Password removed. Sign in with a passkey from now on.' })
      void refreshMe()
    },
  })

  const tooShort = next.length > 0 && [...next].length < 8
  const canSubmit = next.length > 0 && !tooShort && (!me.hasPassword || current.length > 0)
  const submit = (e: FormEvent) => {
    e.preventDefault()
    setDone(undefined)
    if (canSubmit) save.mutate()
  }

  return (
    <Card
      title="Password"
      description={
        me.hasPassword
          ? 'Sign in with your username and password on any device.'
          : 'You sign in with passkeys only. Add a password for devices without one.'
      }
    >
      <form onSubmit={submit} className="mt-4 flex flex-col gap-3">
        {/* Lets password managers save the new password against the right account. */}
        <input type="text" name="username" autoComplete="username" value={me.username} readOnly hidden />
        {me.hasPassword && (
          <Field
            label="Current password"
            secret
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            error={save.error && errorCode(save.error) === 'wrong_password' ? "That isn't your current password." : undefined}
          />
        )}
        <Field
          label={me.hasPassword ? 'New password' : 'Password'}
          secret
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          help="At least 8 characters."
          error={tooShort ? 'At least 8 characters.' : undefined}
        />
        {me.hasPassword && (
          <div className="flex items-center justify-between gap-4 rounded-2xl bg-muted/50 px-4 py-3">
            <div>
              <p className="text-sm font-medium">Sign out other devices</p>
              <p className="text-caption text-muted-foreground">Recommended if someone else might know your old one.</p>
            </div>
            <Switch checked={signOutOthers} onChange={setSignOutOthers} label="Sign out other devices" />
          </div>
        )}
        <Notice>{save.error && errorCode(save.error) !== 'wrong_password' && errorMessage(save.error)}</Notice>
        <Notice tone="success">{done}</Notice>
        <Button type="submit" className="self-end" disabled={!canSubmit || save.isPending}>
          {save.isPending && <LoaderCircle className="animate-spin" />}
          {me.hasPassword ? 'Change password' : 'Set password'}
        </Button>
      </form>

      {me.hasPassword && (
        <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-border/60 pt-4">
          <p className="text-caption text-muted-foreground">
            {me.passkeyCount > 0 ? 'Rather use only passkeys?' : 'Add a passkey before you can remove your password.'}
          </p>
          <ConfirmButton
            label="Remove password"
            confirmLabel="Tap again to remove"
            icon={<Trash2 data-icon="inline-start" />}
            disabled={me.passkeyCount === 0}
            pending={remove.isPending}
            onConfirm={() => remove.mutate()}
          />
        </div>
      )}
      <Notice className="mt-3">{remove.error && errorMessage(remove.error)}</Notice>
    </Card>
  )
}

function errorCode(err: unknown) {
  return err instanceof ApiError ? err.code : undefined
}

// --- Passkeys -----------------------------------------------------------------

function Passkeys() {
  const me = useMe()
  const queryClient = useQueryClient()
  const refreshMe = useRefreshMe()
  const passkeys = useQuery(passkeysQuery)
  const supported = passkeysSupported()
  const [fresh, setFresh] = useState<string>()

  const add = useMutation({
    mutationFn: async () => {
      const ceremony = await unwrap(api.POST('/me/passkeys/begin'))
      const credential = await createPasskey(ceremony.options)
      return unwrap(
        api.POST('/me/passkeys/finish', { body: { ceremonyId: ceremony.ceremonyId, credential, name: deviceName() } }),
      )
    },
    onSuccess: (pk) => {
      queryClient.setQueryData<Passkey[]>(passkeysQuery.queryKey, (list = []) => [...list, pk])
      setFresh(pk.id)
      void refreshMe()
    },
  })
  const addError = add.error instanceof PasskeyCancelled ? null : add.error

  // Your only way to sign in can't be removed.
  const lastCredential = !me.hasPassword && (passkeys.data?.length ?? me.passkeyCount) <= 1

  return (
    <Card
      title="Passkeys"
      description="Sign in with Face ID, a fingerprint or your screen lock. Nothing to remember, and nothing to phish."
    >
      {passkeys.isPending ? (
        <Skeleton className="mt-4 h-16 rounded-2xl" />
      ) : passkeys.isError ? (
        <Notice className="mt-4">{errorMessage(passkeys.error)}</Notice>
      ) : passkeys.data.length === 0 ? (
        <p className="mt-4 rounded-2xl bg-muted/50 px-4 py-3 text-sm text-muted-foreground">No passkeys yet.</p>
      ) : (
        <ul className="mt-4 flex flex-col gap-2">
          <AnimatePresence initial={false}>
            {passkeys.data.map((pk) => (
              <PasskeyRow key={pk.id} passkey={pk} fresh={pk.id === fresh} last={lastCredential} />
            ))}
          </AnimatePresence>
        </ul>
      )}
      <Notice className="mt-4">{addError && errorMessage(addError)}</Notice>
      {supported ? (
        <Button variant="secondary" className="mt-4 self-start" onClick={() => add.mutate()} disabled={add.isPending}>
          {add.isPending ? <LoaderCircle className="animate-spin" /> : <Plus data-icon="inline-start" />}
          Add a passkey
        </Button>
      ) : (
        <p className="mt-4 text-caption text-muted-foreground">This browser can&apos;t create passkeys.</p>
      )}
    </Card>
  )
}

function PasskeyRow({ passkey, fresh, last }: { passkey: Passkey; fresh: boolean; last: boolean }) {
  const queryClient = useQueryClient()
  const refreshMe = useRefreshMe()
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(passkey.name)
  const path = { params: { path: { id: passkey.id } } }

  const rename = useMutation({
    mutationFn: (n: string) => unwrap(api.PATCH('/me/passkeys/{id}', { ...path, body: { name: n } })),
    onSuccess: (_, n) => {
      queryClient.setQueryData<Passkey[]>(passkeysQuery.queryKey, (list) =>
        list?.map((p) => (p.id === passkey.id ? { ...p, name: n } : p)),
      )
      setEditing(false)
    },
  })
  const remove = useMutation({
    mutationFn: () => unwrap(api.DELETE('/me/passkeys/{id}', path)),
    onSuccess: () => {
      queryClient.setQueryData<Passkey[]>(passkeysQuery.queryKey, (list) => list?.filter((p) => p.id !== passkey.id))
      void refreshMe()
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const n = name.trim()
    if (!n) return
    if (n === passkey.name) return setEditing(false)
    rename.mutate(n)
  }

  return (
    <motion.li
      layout
      initial={{ opacity: 0, scale: 0.97 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, height: 0, marginTop: -8 }}
      transition={spring}
      className={cn('overflow-hidden rounded-2xl bg-muted/60 p-3', fresh && 'ring-2 ring-primary/40')}
    >
      {editing ? (
        <form onSubmit={submit} className="flex items-center gap-2">
          <Input
            autoFocus
            aria-label="Passkey name"
            value={name}
            maxLength={64}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && setEditing(false)}
            className="h-9 flex-1"
          />
          <Button type="submit" size="icon-sm" aria-label="Save name" disabled={!name.trim() || rename.isPending}>
            {rename.isPending ? <LoaderCircle className="animate-spin" /> : <Check />}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Cancel"
            onClick={() => {
              setName(passkey.name)
              setEditing(false)
            }}
          >
            <X />
          </Button>
        </form>
      ) : (
        <div className="flex items-center gap-3">
          <span className="grid size-9 shrink-0 place-items-center rounded-xl bg-primary/15 text-primary">
            <KeyRound className="size-4" />
          </span>
          <div className="min-w-0 flex-1">
            <p className="truncate font-medium">{passkey.name}</p>
            <p className="truncate text-caption text-muted-foreground">
              Added {relativeTime(passkey.createdAt)}
              {passkey.lastUsedAt ? ` · used ${relativeTime(passkey.lastUsedAt)}` : ' · never used'}
            </p>
          </div>
          <Button variant="ghost" size="icon-sm" aria-label={`Rename ${passkey.name}`} onClick={() => setEditing(true)}>
            <Pencil />
          </Button>
          {last ? (
            <Button
              variant="ghost"
              size="icon-sm"
              disabled
              aria-label="Can't remove your only way to sign in"
              title="Your only way to sign in. Add a password or another passkey first."
            >
              <Trash2 />
            </Button>
          ) : (
            <ConfirmButton
              compact
              label={`Remove ${passkey.name}`}
              confirmLabel="Remove"
              icon={<Trash2 />}
              pending={remove.isPending}
              onConfirm={() => remove.mutate()}
            />
          )}
        </div>
      )}
      <Notice className="mt-2">{(rename.error || remove.error) && errorMessage(rename.error ?? remove.error)}</Notice>
    </motion.li>
  )
}

// --- Devices ------------------------------------------------------------------

const deviceIcons: Record<DeviceKind, typeof Laptop> = {
  phone: Smartphone,
  tablet: Tablet,
  computer: Laptop,
  unknown: CircleHelp,
}

function Devices() {
  const queryClient = useQueryClient()
  const sessions = useQuery(mySessionsQuery)
  const others = sessions.data?.filter((s) => !s.current) ?? []

  const signOutOthers = useMutation({
    mutationFn: () => unwrap(api.DELETE('/me/sessions')),
    onSuccess: () => {
      queryClient.setQueryData<SignedInSession[]>(mySessionsQuery.queryKey, (list) => list?.filter((s) => s.current))
      toast({ message: 'Signed out everywhere else.' })
    },
  })

  return (
    <Card title="Signed-in devices" description="Sign out anything you don't recognize or don't use anymore.">
      {sessions.isPending ? (
        <div className="mt-4 flex flex-col gap-2">
          <Skeleton className="h-14 rounded-2xl" />
          <Skeleton className="h-14 rounded-2xl" />
        </div>
      ) : sessions.isError ? (
        <Notice className="mt-4">{errorMessage(sessions.error)}</Notice>
      ) : (
        <ul className="mt-4 flex flex-col gap-1">
          <AnimatePresence initial={false}>
            {sessions.data.toSorted((a, b) => Number(b.current) - Number(a.current)).map((s) => (
              <DeviceRow key={s.id} session={s} />
            ))}
          </AnimatePresence>
        </ul>
      )}
      {others.length > 0 && (
        <>
          <Notice className="mt-3">{signOutOthers.error && errorMessage(signOutOthers.error)}</Notice>
          <ConfirmButton
            size="default"
            label={others.length === 1 ? 'Sign out the other device' : `Sign out ${others.length} other devices`}
            confirmLabel="Tap again to sign them out"
            icon={<LogOut data-icon="inline-start" />}
            pending={signOutOthers.isPending}
            onConfirm={() => signOutOthers.mutate()}
            className="mt-3 self-start"
          />
        </>
      )}
    </Card>
  )
}

function DeviceRow({ session }: { session: SignedInSession }) {
  const queryClient = useQueryClient()
  const device = describeDevice(session.userAgent)
  const Icon = deviceIcons[device.kind]

  const revoke = useMutation({
    mutationFn: () => unwrap(api.DELETE('/me/sessions/{id}', { params: { path: { id: session.id } } })),
    onSuccess: () =>
      queryClient.setQueryData<SignedInSession[]>(mySessionsQuery.queryKey, (list) =>
        list?.filter((s) => s.id !== session.id),
      ),
  })

  return (
    <motion.li
      layout
      exit={{ opacity: 0, height: 0 }}
      transition={spring}
      className="overflow-hidden"
    >
      <div className="flex items-center gap-3 py-2">
        <span
          className={cn(
            'grid size-10 shrink-0 place-items-center rounded-xl',
            session.current ? 'bg-primary/15 text-primary' : 'bg-muted text-muted-foreground',
          )}
        >
          <Icon className="size-5" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="flex items-center gap-2 truncate font-medium" title={session.userAgent || undefined}>
            <span className="truncate">{device.label}</span>
            {session.current && <Badge className="shrink-0 px-1.5 py-0 text-[0.65rem]">This device</Badge>}
          </p>
          <p className="truncate text-caption text-muted-foreground">
            {session.current ? 'Active now' : `Active ${relativeTime(session.lastSeenAt)}`} · signed in{' '}
            {relativeTime(session.createdAt)}
          </p>
        </div>
        {!session.current && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => revoke.mutate()}
            disabled={revoke.isPending}
            className="hover:text-destructive"
          >
            {revoke.isPending ? <LoaderCircle className="animate-spin" /> : null}
            Sign out
          </Button>
        )}
      </div>
      <Notice>{revoke.error && errorMessage(revoke.error)}</Notice>
    </motion.li>
  )
}
