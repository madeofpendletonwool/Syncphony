import { useQuery } from '@tanstack/react-query'
import { createFileRoute, Link, type LinkProps } from '@tanstack/react-router'
import {
  ChevronRight,
  Download,
  LoaderCircle,
  LogOut,
  Monitor,
  Moon,
  Palette,
  Share,
  ShieldCheck,
  SquarePlus,
  Sun,
  Users,
  Waypoints,
} from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { api } from '@/api/client'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { useMe, useSignOut, type Me } from '@/lib/auth'
import { laneStyle } from '@/lib/lane'
import { useInstall } from '@/lib/pwa'
import { linksQuery } from '@/lib/services'
import { setPreference, useThemePreference, type ThemePreference } from '@/lib/theme'
import { relativeTime } from '@/lib/time'
import { cn } from '@/lib/utils'

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
        ? `Syncphony ${health.data.version}`
        : health.isError
          ? 'Server unreachable'
          : 'Connecting…'}
    </p>
  )
}
