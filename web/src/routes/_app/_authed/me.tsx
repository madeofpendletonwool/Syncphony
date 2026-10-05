import { useQuery } from '@tanstack/react-query'
import { createFileRoute, Link, type LinkProps } from '@tanstack/react-router'
import { ChevronRight, LoaderCircle, LogOut, Monitor, Moon, Palette, Sun, Users, Waypoints } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { api } from '@/api/client'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { useMe, useSignOut } from '@/lib/auth'
import { laneStyle } from '@/lib/lane'
import { linksQuery } from '@/lib/services'
import { setPreference, useThemePreference, type ThemePreference } from '@/lib/theme'
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
        <section className="glass flex items-center gap-4 rounded-3xl p-5">
          <UserAvatar user={me} ring className="size-14 text-lg" />
          <div className="min-w-0">
            <p className="truncate text-headline">{me.displayName}</p>
            <p className="truncate text-sm text-muted-foreground">@{me.username}</p>
          </div>
          <Badge variant="lane" style={laneStyle(me.color)} className="ml-auto">
            Your lane
          </Badge>
        </section>

        <nav aria-label="Settings" className="glass flex flex-col rounded-3xl p-1.5">
          <ServicesRow />
          {me.role === 'admin' && (
            <SettingsRow to="/settings/people" icon={<Users />}>
              People and invites
            </SettingsRow>
          )}
        </nav>

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

        <SignOutButton />
        <ServerStatus />
      </div>
    </>
  )
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

function SignOutButton() {
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
      Sign out
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
