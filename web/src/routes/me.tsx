import { useQuery } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { ChevronRight, Monitor, Moon, Palette, Sun } from 'lucide-react'
import { api } from '@/api/client'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { laneStyle } from '@/lib/lane'
import { setPreference, useThemePreference, type ThemePreference } from '@/lib/theme'
import { cn } from '@/lib/utils'

export const Route = createFileRoute('/me')({
  component: Me,
})

function Me() {
  const me = useQuery({
    queryKey: ['me'],
    queryFn: async () => {
      const { data } = await api.GET('/me')
      return data ?? null
    },
  })
  const theme = useThemePreference()

  return (
    <>
      <PageHeader title="Me" />
      <div className="flex flex-col gap-4">
        <section className="glass flex items-center gap-4 rounded-3xl p-5">
          {me.isPending ? (
            <>
              <Skeleton className="size-14 rounded-full" />
              <div className="flex flex-col gap-2">
                <Skeleton className="h-4 w-32" />
                <Skeleton className="h-3 w-20" />
              </div>
            </>
          ) : me.data ? (
            <>
              <UserAvatar user={me.data} ring className="size-14 text-lg" />
              <div className="min-w-0">
                <p className="truncate text-headline">{me.data.displayName}</p>
                <p className="truncate text-sm text-muted-foreground">@{me.data.username}</p>
              </div>
              <Badge variant="lane" style={laneStyle(me.data.color)} className="ml-auto">
                Your lane
              </Badge>
            </>
          ) : (
            <p className="text-sm text-muted-foreground">You&apos;re not signed in.</p>
          )}
        </section>

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

        <Link
          to="/design"
          className="glass flex items-center gap-3 rounded-3xl p-5 transition-colors outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <Palette className="size-5 text-primary" />
          <span className="flex-1 font-medium">Design system</span>
          <ChevronRight className="size-4 text-muted-foreground" />
        </Link>

        <ServerStatus />
      </div>
    </>
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
