import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { Check, Copy, LifeBuoy, LoaderCircle, Plus, Share, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useState } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { Notice } from '@/components/notice'
import { PageHeader } from '@/components/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { meQuery, useMe } from '@/lib/auth'
import { easeOutExpo, fadeUp, spring, stagger } from '@/lib/motion'
import { relativeTime } from '@/lib/time'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

type Invite = components['schemas']['Invite']
type Role = components['schemas']['Role']
type ResetLink = components['schemas']['ResetLink']
type User = components['schemas']['User']

export const Route = createFileRoute('/_app/_authed/settings/people')({
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.ensureQueryData(meQuery)
    if (me?.role !== 'admin') throw redirect({ to: '/me', replace: true })
  },
  component: People,
})

const invitesQuery = { queryKey: ['invites'], queryFn: () => unwrap(api.GET('/invites')) }

function People() {
  return (
    <>
      <PageHeader title="People" subtitle="Invite friends and see who's on this server." />
      <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-4">
        <Invites />
        <Members />
      </motion.div>
    </>
  )
}

const expiries = [
  { hours: 24, label: '1 day' },
  { hours: 168, label: '1 week' },
  { hours: 720, label: '30 days' },
] as const

function Invites() {
  const queryClient = useQueryClient()
  const invites = useQuery(invitesQuery)
  const [role, setRole] = useState<Role>('member')
  const [hours, setHours] = useState<number>(168)
  const [fresh, setFresh] = useState<string>()
  // When the page opened; close enough for sorting invites into pending and past.
  const [now] = useState(Date.now)

  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/invites', { body: { role, expiresInHours: hours } })),
    onSuccess: (inv) => {
      queryClient.setQueryData<Invite[]>(invitesQuery.queryKey, (list = []) => [inv, ...list])
      setFresh(inv.code)
    },
  })

  const pending = invites.data?.filter((i) => !i.usedAt && Date.parse(i.expiresAt) > now) ?? []
  const past = invites.data?.filter((i) => !pending.includes(i)).slice(0, 10) ?? []

  return (
    <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-5">
      <h2 className="text-headline">Invite someone</h2>
      <p className="mt-1 text-sm text-muted-foreground">Each link works once. Send it however you like.</p>

      <div className="mt-4 flex flex-col gap-3">
        <OptionRow label="Role">
          <ToggleGroup type="single" value={role} onValueChange={(v) => v && setRole(v as Role)} aria-label="Role">
            <ToggleGroupItem value="member">Member</ToggleGroupItem>
            <ToggleGroupItem value="admin">Admin</ToggleGroupItem>
          </ToggleGroup>
        </OptionRow>
        <OptionRow label="Expires">
          <ToggleGroup
            type="single"
            value={String(hours)}
            onValueChange={(v) => v && setHours(Number(v))}
            aria-label="Expires after"
          >
            {expiries.map((e) => (
              <ToggleGroupItem key={e.hours} value={String(e.hours)}>
                {e.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </OptionRow>
      </div>
      {role === 'admin' && (
        <p className="mt-3 text-caption text-muted-foreground">Admins can invite people and revoke invites.</p>
      )}

      <Notice className="mt-4">{create.error && errorMessage(create.error)}</Notice>
      <Button className="mt-4" onClick={() => create.mutate()} disabled={create.isPending}>
        {create.isPending ? <LoaderCircle className="animate-spin" /> : <Plus data-icon="inline-start" />}
        Create invite link
      </Button>

      {invites.isPending ? (
        <Skeleton className="mt-5 h-16 rounded-2xl" />
      ) : invites.isError ? (
        <Notice className="mt-5">{errorMessage(invites.error)}</Notice>
      ) : (
        <>
          {pending.length > 0 && (
            <>
              <h3 className="mt-6 mb-2 px-1 text-caption font-medium text-muted-foreground uppercase tracking-wide">
                Waiting to be used
              </h3>
              <ul className="flex flex-col gap-2">
                <AnimatePresence initial={false}>
                  {pending.map((inv) => (
                    <InviteRow key={inv.code} invite={inv} fresh={inv.code === fresh} />
                  ))}
                </AnimatePresence>
              </ul>
            </>
          )}
          {past.length > 0 && (
            <>
              <h3 className="mt-6 mb-2 px-1 text-caption font-medium text-muted-foreground uppercase tracking-wide">
                Recent
              </h3>
              <ul className="flex flex-col gap-1">
                {past.map((inv) => (
                  <PastInvite key={inv.code} invite={inv} />
                ))}
              </ul>
            </>
          )}
        </>
      )}
    </motion.section>
  )
}

function OptionRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2">
      <span className="text-sm font-medium">{label}</span>
      {children}
    </div>
  )
}

function InviteRow({ invite, fresh }: { invite: Invite; fresh: boolean }) {
  const queryClient = useQueryClient()
  const [copied, setCopied] = useState(false)
  const canShare = typeof navigator !== 'undefined' && 'share' in navigator

  const revoke = useMutation({
    mutationFn: () => unwrap(api.DELETE('/invites/{code}', { params: { path: { code: invite.code } } })),
    onSuccess: () =>
      queryClient.setQueryData<Invite[]>(invitesQuery.queryKey, (list) => list?.filter((i) => i.code !== invite.code)),
  })

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(invite.url)
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    } catch {
      // Clipboard blocked (http, or denied); the link is selectable below.
    }
  }

  const share = () =>
    navigator.share({ title: 'Join me on Syncphony', text: 'Add songs to our queue:', url: invite.url }).catch(() => {})

  return (
    <motion.li
      layout
      initial={{ opacity: 0, scale: 0.97 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, height: 0, marginTop: -8 }}
      transition={spring}
      className={cn('overflow-hidden rounded-2xl bg-muted/60 p-3', fresh && 'ring-2 ring-primary/40')}
    >
      <div className="flex items-center gap-2">
        <div className="min-w-0 flex-1">
          <p className="truncate font-mono text-[0.8rem] select-all" title={invite.url}>
            {invite.url.replace(/^https?:\/\//, '')}
          </p>
          <p className="mt-0.5 flex items-center gap-1.5 text-caption text-muted-foreground">
            {invite.role === 'admin' && <Badge className="px-1.5 py-0 text-[0.65rem]">Admin</Badge>}
            Expires {relativeTime(invite.expiresAt)}
          </p>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="Copy invite link" onClick={copy}>
          <AnimatePresence mode="wait" initial={false}>
            <motion.span
              key={copied ? 'done' : 'copy'}
              initial={{ scale: 0.6, opacity: 0 }}
              animate={{ scale: 1, opacity: 1 }}
              exit={{ scale: 0.6, opacity: 0 }}
              transition={{ duration: 0.15, ease: easeOutExpo }}
            >
              {copied ? <Check className="size-4 text-success" /> : <Copy className="size-4" />}
            </motion.span>
          </AnimatePresence>
        </Button>
        {canShare && (
          <Button variant="ghost" size="icon-sm" aria-label="Share invite link" onClick={share}>
            <Share />
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Revoke invite"
          onClick={() => revoke.mutate()}
          disabled={revoke.isPending}
          className="hover:text-destructive"
        >
          {revoke.isPending ? <LoaderCircle className="animate-spin" /> : <X />}
        </Button>
      </div>
      <Notice className="mt-2">{revoke.error && errorMessage(revoke.error)}</Notice>
    </motion.li>
  )
}

function PastInvite({ invite }: { invite: Invite }) {
  const users = useQuery(usersQuery)
  const usedBy = invite.usedBy && users.data?.find((u) => u.id === invite.usedBy)
  return (
    <li className="flex items-center justify-between gap-3 px-1 py-1.5 text-sm">
      <span className="min-w-0 truncate text-muted-foreground">
        {invite.usedAt
          ? `Used by ${usedBy ? usedBy.displayName : 'someone'} ${relativeTime(invite.usedAt)}`
          : `Expired ${relativeTime(invite.expiresAt)}`}
      </span>
      <Badge variant="outline">{invite.usedAt ? 'Used' : 'Expired'}</Badge>
    </li>
  )
}

function Members() {
  const me = useMe()
  const users = useQuery(usersQuery)
  const resets = useQuery(resetLinksQuery)
  const pendingReset = new Map(resets.data?.map((l) => [l.userId, l]))
  // The member whose reset panel is open.
  const [recovering, setRecovering] = useState<string>()

  return (
    <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-5">
      <h2 className="text-headline">
        Members
        {users.data && <span className="ml-2 text-muted-foreground tabular-nums">{users.data.length}</span>}
      </h2>
      {users.isPending ? (
        <div className="mt-4 flex flex-col gap-3">
          <Skeleton className="h-12 rounded-2xl" />
          <Skeleton className="h-12 rounded-2xl" />
        </div>
      ) : users.isError ? (
        <Notice className="mt-4">{errorMessage(users.error)}</Notice>
      ) : (
        <ul className="mt-3 flex flex-col">
          {users.data.map((u) => (
            <li key={u.id} className="flex flex-col py-2">
              <div className="flex items-center gap-3">
                <UserAvatar user={u} className="size-10" />
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium">
                    {u.displayName}
                    {u.id === me.id && <span className="ml-1.5 text-muted-foreground">(you)</span>}
                  </p>
                  <p className="truncate text-caption text-muted-foreground">
                    {u.guest ? 'Guest' : `@${u.username}`} · joined {relativeTime(u.createdAt)}
                  </p>
                </div>
                {u.role === 'admin' && <Badge>Admin</Badge>}
                {u.id !== me.id && !u.guest && (
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`Reset link for ${u.displayName}`}
                    title="Locked out? Make a reset link"
                    aria-expanded={recovering === u.id}
                    onClick={() => setRecovering((id) => (id === u.id ? undefined : u.id))}
                  >
                    <LifeBuoy />
                  </Button>
                )}
              </div>
              <AnimatePresence initial={false}>
                {(recovering === u.id || pendingReset.has(u.id)) && !u.guest && u.id !== me.id && (
                  <ResetPanel
                    user={u}
                    pending={pendingReset.get(u.id)}
                    onClose={() => setRecovering(undefined)}
                  />
                )}
              </AnimatePresence>
            </li>
          ))}
        </ul>
      )}
    </motion.section>
  )
}

// --- Reset links ----------------------------------------------------------------

const resetLinksQuery = { queryKey: ['reset-links'], queryFn: () => unwrap(api.GET('/reset-links')) }

const resetExpiries = [
  { hours: 1, label: '1 hour' },
  { hours: 24, label: '1 day' },
  { hours: 168, label: '1 week' },
] as const

/**
 * Makes a one-time link for a member who can't sign in. The link is shown
 * only right after it's made: the server keeps just its hash.
 */
function ResetPanel({ user, pending, onClose }: { user: User; pending?: ResetLink; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [hours, setHours] = useState<number>(24)
  const [fresh, setFresh] = useState<ResetLink>()
  const [copied, setCopied] = useState(false)
  const canShare = typeof navigator !== 'undefined' && 'share' in navigator
  const first = user.displayName.split(/\s+/)[0]
  const path = { params: { path: { id: user.id } } }

  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/users/{id}/reset-link', { ...path, body: { expiresInHours: hours } })),
    onSuccess: (link) => {
      setFresh(link)
      queryClient.setQueryData<ResetLink[]>(resetLinksQuery.queryKey, (list = []) => [
        { ...link, url: undefined },
        ...list.filter((l) => l.userId !== user.id),
      ])
    },
  })
  const revoke = useMutation({
    mutationFn: () => unwrap(api.DELETE('/users/{id}/reset-link', path)),
    onSuccess: () => {
      setFresh(undefined)
      queryClient.setQueryData<ResetLink[]>(resetLinksQuery.queryKey, (list) => list?.filter((l) => l.userId !== user.id))
      onClose()
    },
  })

  const url = fresh?.url
  const copy = async () => {
    if (!url) return
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    } catch {
      // Clipboard blocked; the link is selectable.
    }
  }
  const share = () =>
    url && navigator.share({ title: 'Get back into Syncphony', text: `${first}, use this to get back in:`, url }).catch(() => {})

  return (
    <motion.div
      initial={{ opacity: 0, height: 0 }}
      animate={{ opacity: 1, height: 'auto' }}
      exit={{ opacity: 0, height: 0 }}
      transition={{ duration: 0.3, ease: easeOutExpo }}
      className="overflow-hidden"
    >
      <div className="mt-2 ml-13 flex flex-col gap-3 rounded-2xl bg-muted/60 p-3">
        {url ? (
          <>
            <div className="flex items-center gap-2">
              <p className="min-w-0 flex-1 truncate font-mono text-[0.8rem] select-all" title={url}>
                {url.replace(/^https?:\/\//, '')}
              </p>
              <Button variant="ghost" size="icon-sm" aria-label="Copy reset link" onClick={copy}>
                {copied ? <Check className="size-4 text-success" /> : <Copy className="size-4" />}
              </Button>
              {canShare && (
                <Button variant="ghost" size="icon-sm" aria-label="Share reset link" onClick={share}>
                  <Share />
                </Button>
              )}
            </div>
            <p className="text-caption text-muted-foreground">
              Send this to {first} yourself. It works once, until it expires {relativeTime(fresh.expiresAt)}, and signs{' '}
              {first} out everywhere else. You won&apos;t be able to see it again.
            </p>
          </>
        ) : pending ? (
          <p className="text-sm">
            A reset link for {first} is waiting to be used. It expires {relativeTime(pending.expiresAt)}.
            <span className="block text-caption text-muted-foreground">
              Lost it? Make a new one; the old one stops working.
            </span>
          </p>
        ) : (
          <p className="text-sm">
            Locked out? Make a one-time link that lets {first} set a new password or passkey.
          </p>
        )}

        {!url && (
          <div className="flex flex-wrap items-center justify-between gap-2">
            <ToggleGroup
              type="single"
              value={String(hours)}
              onValueChange={(v) => v && setHours(Number(v))}
              aria-label="Reset link expires after"
            >
              {resetExpiries.map((e) => (
                <ToggleGroupItem key={e.hours} value={String(e.hours)}>
                  {e.label}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </div>
        )}
        <Notice>{(create.error || revoke.error) && errorMessage(create.error ?? revoke.error)}</Notice>
        <div className="flex flex-wrap gap-2">
          {!url && (
            <Button size="sm" onClick={() => create.mutate()} disabled={create.isPending}>
              {create.isPending ? <LoaderCircle className="animate-spin" /> : <LifeBuoy data-icon="inline-start" />}
              {pending ? 'Make a new link' : 'Make reset link'}
            </Button>
          )}
          {(pending || url) && (
            <Button variant="ghost" size="sm" onClick={() => revoke.mutate()} disabled={revoke.isPending} className="hover:text-destructive">
              {revoke.isPending ? <LoaderCircle className="animate-spin" /> : <X data-icon="inline-start" />}
              Cancel link
            </Button>
          )}
          {!pending && !url && (
            <Button variant="ghost" size="sm" onClick={onClose}>
              Not now
            </Button>
          )}
        </div>
      </div>
    </motion.div>
  )
}
