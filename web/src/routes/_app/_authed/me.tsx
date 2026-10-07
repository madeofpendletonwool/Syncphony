import { useMutation, useQuery } from '@tanstack/react-query'
import { createFileRoute, Link, type LinkProps } from '@tanstack/react-router'
import {
  ChevronRight,
  Download,
  HardDrive,
  LoaderCircle,
  LogOut,
  Monitor,
  Moon,
  Palette,
  Share,
  KeyRound,
  ShieldCheck,
  SquarePlus,
  Sun,
  Trash2,
  Users,
  Waypoints,
} from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState, type ReactNode } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { Field } from '@/components/field'
import { Notice } from '@/components/notice'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { useMe, useSignedOut, useSignOut, type Me } from '@/lib/auth'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo } from '@/lib/motion'
import { useInstall } from '@/lib/pwa'
import { appTitle, serverSettingsQuery } from '@/lib/server'
import { linksQuery } from '@/lib/services'
import { setPreference, useThemePreference, type ThemePreference } from '@/lib/theme'
import { relativeTime } from '@/lib/time'
import { cn } from '@/lib/utils'
import { getPasskey, PasskeyCancelled, passkeysSupported } from '@/lib/webauthn'

export const Route = createFileRoute('/_app/_authed/me')({
  component: Me,
})

function Me() {
  const me = useMe()
  const theme = useThemePreference()

  return (
    <>
      <PageHeader title="Me" />
      <div className="flex flex-col gap-4">
        {me.guest ? (
          <section className="glass flex items-center gap-4 rounded-3xl p-5">
            <ProfileSummary me={me} />
          </section>
        ) : (
          <Link
            to="/settings/profile"
            aria-label="Edit profile"
            className="glass flex items-center gap-4 rounded-3xl p-5 transition-colors outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <ProfileSummary me={me} />
            <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
          </Link>
        )}

        {me.guest ? (
          <p className="glass rounded-3xl p-5 text-sm text-muted-foreground">
            You joined as a guest, so there&apos;s nothing to set up. Your pass ends {relativeTime(me.guest.expiresAt)}; after that
            your picks stay in the room&apos;s history under your name.
          </p>
        ) : (
          <>
            <InstallCard />

            <nav aria-label="Settings" className="glass flex flex-col rounded-3xl p-1.5">
              <SettingsRow to="/settings/security" icon={<ShieldCheck />} trailing={<SignInSummary me={me} />}>
                Sign-in &amp; security
              </SettingsRow>
              <ServicesRow />
              {me.role === 'admin' && (
                <SettingsRow to="/settings/people" icon={<Users />}>
                  People and invites
                </SettingsRow>
              )}
              {me.role === 'admin' && (
                <SettingsRow to="/settings/server" icon={<HardDrive />}>
                  Server
                </SettingsRow>
              )}
            </nav>
          </>
        )}

        <section className="glass rounded-3xl p-5">
          <h2 className="text-headline">Appearance</h2>
          <p className="mt-1 text-sm text-muted-foreground">Colors follow whatever&apos;s playing.</p>
          <ToggleGroup
            type="single"
            value={theme}
            onValueChange={(v) => v && setPreference(v as ThemePreference)}
            aria-label="Theme"
            className="mt-4"
          >
            <ToggleGroupItem value="dark">
              <Moon /> Dark
            </ToggleGroupItem>
            <ToggleGroupItem value="light">
              <Sun /> Light
            </ToggleGroupItem>
            <ToggleGroupItem value="system">
              <Monitor /> System
            </ToggleGroupItem>
          </ToggleGroup>
        </section>

        <nav aria-label="More" className="glass flex flex-col rounded-3xl p-1.5">
          <SettingsRow to="/design" icon={<Palette />}>
            Design system
          </SettingsRow>
        </nav>

        <SignOutButton guest={!!me.guest} />
        {!me.guest && <DeleteAccount me={me} />}
        <ServerStatus />
      </div>
    </>
  )
}

function ProfileSummary({ me }: { me: Me }) {
  return (
    <>
      <UserAvatar user={me} ring className="size-14 text-lg" />
      <div className="min-w-0">
        <p className="truncate text-headline">{me.displayName}</p>
        {!me.guest && <p className="truncate text-sm text-muted-foreground">@{me.username}</p>}
      </div>
      <Badge variant="lane" style={laneStyle(me.color)} className="ml-auto">
        {me.guest ? 'Guest' : 'Your lane'}
      </Badge>
    </>
  )
}

/** How you sign in, in a few words: "Password, 2 passkeys". */
function SignInSummary({ me }: { me: Me }) {
  const parts = [
    me.hasPassword && 'Password',
    me.passkeyCount > 0 && (me.passkeyCount === 1 ? '1 passkey' : `${me.passkeyCount} passkeys`),
  ].filter(Boolean)
  return <span className="hidden text-sm text-muted-foreground min-[380px]:inline">{parts.join(', ')}</span>
}

function SettingsRow({ to, icon, children, trailing }: { to: LinkProps['to']; icon: ReactNode; children: ReactNode; trailing?: ReactNode }) {
  return (
    <Link
      to={to}
      className="flex items-center gap-3 rounded-[1.4rem] px-3.5 py-3.5 transition-colors outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50 [&>svg:first-child]:size-5 [&>svg:first-child]:text-primary"
    >
      {icon}
      <span className="flex-1 font-medium">{children}</span>
      {trailing}
      <ChevronRight className="size-4 text-muted-foreground" />
    </Link>
  )
}

/** Services, with a heads-up when a link needs attention. */
function ServicesRow() {
  const links = useQuery(linksQuery)
  const broken = links.data?.filter((l) => l.status !== 'ok').length ?? 0
  const count = links.data?.length ?? 0
  return (
    <SettingsRow
      to="/settings/services"
      icon={<Waypoints />}
      trailing={
        broken > 0 ? (
          <Badge variant="destructive">{broken === 1 ? '1 needs attention' : `${broken} need attention`}</Badge>
        ) : links.isSuccess ? (
          <span className="text-sm text-muted-foreground">{count === 0 ? 'None linked' : `${count} linked`}</span>
        ) : null
      }
    >
      Services
    </SettingsRow>
  )
}

/** Install Syncphony as an app: full screen, on the Home Screen, and a steadier speaker. */
function InstallCard() {
  const state = useInstall()
  if (state.kind === 'installed' || state.kind === 'unavailable') return null
  return (
    <section className="glass flex flex-col gap-3 rounded-3xl p-5">
      <div className="flex items-start gap-3">
        <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary/15 text-primary">
          <Download className="size-5" />
        </span>
        <div>
          <h2 className="font-medium">Install Syncphony</h2>
          <p className="text-sm text-muted-foreground">Open it from your Home Screen, full screen, like any other app.</p>
        </div>
      </div>
      {state.kind === 'prompt' ? (
        <Button onClick={() => void state.install()} className="self-start">
          <Download data-icon="inline-start" />
          Install
        </Button>
      ) : (
        <ol className="flex flex-col gap-2 text-sm">
          <li className="flex items-center gap-2">
            <span className="grid size-6 place-items-center rounded-full bg-muted text-caption font-medium">1</span>
            Tap <Share className="size-4 text-primary" aria-label="Share" /> in Safari&apos;s toolbar
          </li>
          <li className="flex items-center gap-2">
            <span className="grid size-6 place-items-center rounded-full bg-muted text-caption font-medium">2</span>
            Choose <SquarePlus className="size-4 text-primary" aria-hidden /> <span className="font-medium">Add to Home Screen</span>
          </li>
        </ol>
      )}
    </section>
  )
}

function SignOutButton({ guest }: { guest: boolean }) {
  const signOut = useSignOut()
  const [pending, setPending] = useState(false)
  return (
    <Button
      variant="glass"
      size="lg"
      disabled={pending}
      onClick={() => {
        setPending(true)
        signOut().finally(() => setPending(false))
      }}
    >
      {pending ? <LoaderCircle className="animate-spin" /> : <LogOut data-icon="inline-start" />}
      {guest ? 'Leave' : 'Sign out'}
    </Button>
  )
}

function ServerStatus() {
  const name = useQuery(serverSettingsQuery).data?.instanceName
  const health = useQuery({
    queryKey: ['health'],
    queryFn: async () => {
      const { data, error } = await api.GET('/healthz')
      if (error || !data) throw new Error('server unreachable')
      return data
    },
    refetchInterval: 10_000,
  })

  return (
    <p className="flex items-center justify-center gap-2 py-4 text-caption text-muted-foreground">
      <span
        className={cn(
          'size-1.5 rounded-full',
          health.isSuccess ? 'bg-success' : health.isError ? 'bg-destructive' : 'bg-muted-foreground',
        )}
      />
      {health.isSuccess
        ? `${appTitle(name)} ${health.data.version}`
        : health.isError
          ? 'Server unreachable'
          : 'Connecting…'}
    </p>
  )
}

/**
 * Deletes your account, once you confirm it's you with your password or a
 * passkey. It's tucked away behind a quiet link: it can't be undone.
 */
function DeleteAccount({ me }: { me: Me }) {
  const signedOut = useSignedOut()
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')
  const withPasskey = me.passkeyCount > 0 && passkeysSupported()

  const remove = useMutation({
    mutationFn: async (how: 'password' | 'passkey') => {
      if (how === 'password') {
        return unwrap(api.POST('/me/delete', { body: { password } }))
      }
      const ceremony = await unwrap(api.POST('/me/reauth/begin'))
      const credential = await getPasskey(ceremony.options)
      return unwrap(api.POST('/me/delete', { body: { ceremonyId: ceremony.ceremonyId, credential } }))
    },
    onSuccess: () => void signedOut(),
  })
  const error = remove.error instanceof PasskeyCancelled ? null : remove.error

  return (
    <section className="flex flex-col items-center">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="rounded-lg px-2 py-1 text-caption text-muted-foreground transition-colors outline-none hover:text-destructive focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        Delete my account
      </button>
      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            initial={{ opacity: 0, height: 0 }}
            animate={{ opacity: 1, height: 'auto' }}
            exit={{ opacity: 0, height: 0 }}
            transition={{ duration: 0.3, ease: easeOutExpo }}
            className="w-full overflow-hidden"
          >
            <form
              className="glass mt-2 flex flex-col gap-4 rounded-3xl p-5"
              onSubmit={(e) => {
                e.preventDefault()
                remove.mutate('password')
              }}
            >
              <div>
                <h2 className="text-headline">Delete your account?</h2>
                <p className="mt-1 text-sm text-muted-foreground">
                  You&apos;ll be signed out everywhere. Your sign-in, linked services and their saved credentials, and your
                  picture are deleted, and your waiting songs leave the queue. Rooms you own go to an admin. Songs you played
                  stay in rooms&apos; history as &ldquo;Former member&rdquo;. This can&apos;t be undone.
                </p>
              </div>
              {me.hasPassword && (
                <Field
                  label="Your password"
                  secret
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              )}
              {!me.hasPassword && !withPasskey && (
                <p className="text-sm text-muted-foreground">Open Syncphony on a device with your passkey to confirm it&apos;s you.</p>
              )}
              <Notice>{error && errorMessage(error)}</Notice>
              <div className="flex flex-wrap gap-2">
                {me.hasPassword && (
                  <Button type="submit" variant="destructive" disabled={remove.isPending || !password}>
                    {remove.isPending && remove.variables === 'password' ? <LoaderCircle className="animate-spin" /> : <Trash2 data-icon="inline-start" />}
                    Delete my account
                  </Button>
                )}
                {withPasskey && (
                  <Button type="button" variant={me.hasPassword ? 'ghost' : 'destructive'} disabled={remove.isPending} onClick={() => remove.mutate('passkey')}>
                    {remove.isPending && remove.variables === 'passkey' ? <LoaderCircle className="animate-spin" /> : <KeyRound data-icon="inline-start" />}
                    {me.hasPassword ? 'Use a passkey instead' : 'Confirm with a passkey'}
                  </Button>
                )}
                <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
                  Keep it
                </Button>
              </div>
            </form>
          </motion.div>
        )}
      </AnimatePresence>
    </section>
  )
}
