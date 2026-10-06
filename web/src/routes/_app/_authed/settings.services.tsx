import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, redirect, useNavigate } from '@tanstack/react-router'
import { ArrowRight, Check, ExternalLink, LoaderCircle, Plus, RefreshCw, Unlink, Users } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { Field } from '@/components/field'
import { Notice } from '@/components/notice'
import { PageHeader } from '@/components/page-header'
import { ProviderIcon } from '@/components/provider-icon'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { UserAvatar } from '@/components/user-avatar'
import { meQuery, useMe } from '@/lib/auth'
import { easeOutExpo, fadeUp, stagger } from '@/lib/motion'
import { linksQuery, providersQuery, sourceName, usableLinksQuery } from '@/lib/services'
import { relativeTime } from '@/lib/time'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

type Provider = components['schemas']['ProviderInfo']
type ServiceLink = components['schemas']['ServiceLink']

type Search = { welcome?: boolean; linked?: string; link_error?: string }

// The server's OAuth2 callback lands here with ?linked= or ?link_error=.
export const Route = createFileRoute('/_app/_authed/settings/services')({
  // Guests use the room's shared services; they have none of their own.
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.ensureQueryData(meQuery)
    if (me?.guest) throw redirect({ to: '/me', replace: true })
  },
  validateSearch: (search: Record<string, unknown>): Search => ({
    welcome: search.welcome === true || search.welcome === 'true' || undefined,
    linked: typeof search.linked === 'string' ? search.linked : undefined,
    link_error: typeof search.link_error === 'string' ? search.link_error : undefined,
  }),
  component: Services,
})

function Services() {
  const search = Route.useSearch()
  const navigate = useNavigate()
  const providers = useQuery(providersQuery)
  const links = useQuery(linksQuery)

  // Show the OAuth outcome once, then drop it from the URL so a reload
  // doesn't repeat it.
  const [outcome] = useState(() => ({ linked: search.linked, error: search.link_error }))
  const [highlight, setHighlight] = useState(search.linked)
  useEffect(() => {
    if (search.linked || search.link_error) {
      void navigate({ to: '.', search: { welcome: search.welcome }, replace: true })
    }
  }, [navigate, search.linked, search.link_error, search.welcome])

  const linkedLabel = outcome.linked && links.data?.find((l) => l.id === outcome.linked)?.accountLabel
  const hasLinks = (links.data?.length ?? 0) > 0

  return (
    <>
      <PageHeader
        title="Services"
        subtitle="Link the accounts you play music from, and share your own libraries with everyone here. Your passwords stay on the server."
      />

      <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-4">
        <Notice tone="success">{outcome.linked && (linkedLabel ? `Linked ${linkedLabel}.` : 'Linked.')}</Notice>
        <Notice>{outcome.error && errorMessage(outcome.error)}</Notice>

        {search.welcome && (
          <motion.section variants={fadeUp} className="glass flex flex-col gap-3 rounded-3xl p-5">
            <h2 className="text-headline">{hasLinks ? "You're all set" : "You're in! One more step"}</h2>
            <p className="text-sm text-muted-foreground">
              {hasLinks
                ? 'Find something to play and add it to your lane.'
                : 'Link a service you have an account with, so you can search it and add songs.'}
            </p>
            <Button asChild variant={hasLinks ? 'default' : 'ghost'} className="self-start">
              <Link to="/room">
                {hasLinks ? 'Go to the room' : 'Skip for now'}
                <ArrowRight data-icon="inline-end" />
              </Link>
            </Button>
          </motion.section>
        )}

        {providers.isPending || links.isPending ? (
          <>
            <Skeleton className="h-36 rounded-3xl" />
            <Skeleton className="h-36 rounded-3xl" />
          </>
        ) : providers.isError || links.isError ? (
          <Notice>{errorMessage(providers.error ?? links.error)}</Notice>
        ) : (
          <>
            {providers.data.map((p) => (
              <ProviderCard
                key={p.id}
                provider={p}
                links={links.data.filter((l) => l.provider === p.id)}
                highlight={highlight}
                onLinked={setHighlight}
              />
            ))}
            <OrphanLinks links={links.data.filter((l) => !providers.data.some((p) => p.id === l.provider))} />
            <SharedWithYou providers={providers.data} />
          </>
        )}
      </motion.div>
    </>
  )
}

function ProviderCard({
  provider,
  links,
  highlight,
  onLinked,
}: {
  provider: Provider
  links: ServiceLink[]
  highlight?: string
  onLinked: (id: string) => void
}) {
  const [adding, setAdding] = useState(false)
  const oauth = useBeginOAuth()

  // Pairing providers and credentials providers open a panel; plain OAuth2
  // providers go straight to the service.
  const startLink = () => {
    if (provider.linkMethod === 'oauth2' && !provider.pairing) oauth.mutate({ provider: provider.id })
    else setAdding(true)
  }

  return (
    <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-5">
      <header className="flex items-center gap-3">
        <ProviderIcon icon={provider.icon} />
        <div className="min-w-0 flex-1">
          <h2 className="text-headline">{provider.name}</h2>
          <p className="text-caption text-muted-foreground">
            {provider.playback === 'stream' ? 'Plays through Syncphony' : `Plays in the ${provider.name} app`}
          </p>
        </div>
        {links.length > 0 && !adding && (
          <Button variant="ghost" size="sm" onClick={startLink} disabled={oauth.isPending}>
            <Plus data-icon="inline-start" />
            Add
          </Button>
        )}
      </header>

      {links.length > 0 && (
        <ul className="mt-4 flex flex-col gap-2">
          {links.map((l) => (
            <LinkRow key={l.id} link={l} provider={provider} highlighted={l.id === highlight} />
          ))}
        </ul>
      )}

      <Notice className="mt-4">{oauth.error && errorMessage(oauth.error)}</Notice>

      <AnimatePresence initial={false}>
        {adding && (
          <Expand key="form">
            {provider.pairing ? (
              <PairingPanel
                provider={provider}
                onDone={(link) => {
                  setAdding(false)
                  if (link) onLinked(link.id)
                }}
              />
            ) : (
              <LinkForm
                provider={provider}
                onDone={(link) => {
                  setAdding(false)
                  if (link) onLinked(link.id)
                }}
              />
            )}
          </Expand>
        )}
      </AnimatePresence>

      {links.length === 0 && !adding && (
        <Button className="mt-4" onClick={startLink} disabled={oauth.isPending}>
          {oauth.isPending ? (
            <LoaderCircle className="animate-spin" />
          ) : (
            provider.linkMethod === 'oauth2' && !provider.pairing && <ExternalLink data-icon="inline-start" />
          )}
          Link {provider.name}
        </Button>
      )}
    </motion.section>
  )
}

const statusText: Record<ServiceLink['status'], string> = {
  ok: 'Working',
  needs_relink: 'Needs linking again',
  error: "Can't reach it",
}

function LinkRow({ link, provider, highlighted }: { link: ServiceLink; provider?: Provider; highlighted: boolean }) {
  const queryClient = useQueryClient()
  const [mode, setMode] = useState<'idle' | 'relink' | 'pair' | 'unlink'>('idle')
  const oauth = useBeginOAuth()
  const unlink = useMutation({
    mutationFn: () => unwrap(api.DELETE('/links/{id}', { params: { path: { id: link.id } } })),
    onSuccess: () => queryClient.setQueryData(linksQuery.queryKey, (ls) => ls?.filter((l) => l.id !== link.id)),
  })

  const relink = () => {
    if (!provider) return
    if (provider.pairing) setMode('pair')
    else if (provider.linkMethod === 'oauth2') oauth.mutate({ linkId: link.id })
    else setMode('relink')
  }
  const healthy = link.status === 'ok'

  return (
    <motion.li
      layout
      className={cn(
        'rounded-2xl bg-muted/60 p-3 transition-shadow duration-700',
        highlighted && 'ring-2 ring-success/50',
        link.status === 'needs_relink' && 'ring-1 ring-amber-500/40',
      )}
    >
      <div className="flex items-center gap-3">
        <span
          aria-hidden
          className={cn(
            'size-2 shrink-0 rounded-full',
            healthy ? 'bg-success shadow-[0_0_8px_var(--success)]' : link.status === 'needs_relink' ? 'bg-amber-500' : 'bg-destructive',
          )}
        />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">{link.accountLabel}</p>
          <p className={cn('truncate text-caption', healthy ? 'text-muted-foreground' : link.status === 'needs_relink' ? 'text-amber-600 dark:text-amber-400' : 'text-destructive')}>
            {statusText[link.status]}
            {healthy && link.lastOkAt && ` · checked ${relativeTime(link.lastOkAt)}`}
            {!healthy && link.statusDetail && ` · ${link.statusDetail}`}
          </p>
        </div>
        {mode === 'idle' && (
          <div className="flex shrink-0 gap-1">
            {provider && (
            <Button
              variant={healthy ? 'ghost' : 'default'}
              size={healthy ? 'icon-sm' : 'sm'}
              aria-label={`Link ${link.accountLabel} again`}
              onClick={relink}
              disabled={oauth.isPending}
            >
              {oauth.isPending ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
              {!healthy && 'Re-link'}
            </Button>
            )}
            <Button variant="ghost" size="icon-sm" aria-label={`Unlink ${link.accountLabel}`} onClick={() => setMode('unlink')}>
              <Unlink />
            </Button>
          </div>
        )}
      </div>

      {provider?.capabilities.shareable && <ShareSwitch link={link} />}

      <Notice className="mt-3">{(oauth.error || unlink.error) && errorMessage(oauth.error ?? unlink.error)}</Notice>

      <AnimatePresence initial={false}>
        {mode === 'unlink' && (
          <Expand key="unlink">
            <p className="text-sm text-muted-foreground">
              Unlink this account? Its stored login is deleted, and songs you queued from it won&apos;t play.
            </p>
            <div className="mt-3 flex justify-end gap-2">
              <Button variant="ghost" size="sm" onClick={() => setMode('idle')}>
                Cancel
              </Button>
              <Button variant="destructive" size="sm" onClick={() => unlink.mutate()} disabled={unlink.isPending}>
                {unlink.isPending && <LoaderCircle className="animate-spin" />}
                Unlink
              </Button>
            </div>
          </Expand>
        )}
        {mode === 'relink' && provider && (
          <Expand key="relink">
            <LinkForm provider={provider} link={link} onDone={() => setMode('idle')} />
          </Expand>
        )}
        {mode === 'pair' && provider && (
          <Expand key="pair">
            <PairingPanel provider={provider} link={link} onDone={() => setMode('idle')} />
          </Expand>
        )}
      </AnimatePresence>
    </motion.li>
  )
}

/**
 * The link form for a `credentials` provider, generated from its fields.
 * With `link`, it re-links that account instead of adding one.
 */
function LinkForm({
  provider,
  link,
  onDone,
}: {
  provider: Provider
  link?: ServiceLink
  onDone: (link?: ServiceLink) => void
}) {
  const queryClient = useQueryClient()
  const [values, setValues] = useState<Record<string, string>>({})

  const save = useMutation({
    mutationFn: () => {
      const fields = Object.fromEntries(provider.fields.map((f) => [f.name, (values[f.name] ?? '').trim()]))
      return link
        ? unwrap(api.PUT('/links/{id}', { params: { path: { id: link.id } }, body: { fields } }))
        : unwrap(api.POST('/links', { body: { provider: provider.id, fields } }))
    },
    onSuccess: (saved) => {
      queryClient.setQueryData(linksQuery.queryKey, (ls = []) =>
        ls.some((l) => l.id === saved.id) ? ls.map((l) => (l.id === saved.id ? saved : l)) : [...ls, saved],
      )
      onDone(saved)
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    save.mutate()
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      {link && <p className="text-sm text-muted-foreground">Sign in to the same account again.</p>}
      {provider.fields.map((f, i) => (
        <Field
          key={f.name}
          label={f.label}
          name={f.name}
          secret={f.kind === 'secret'}
          type={f.kind === 'url' ? 'url' : 'text'}
          inputMode={f.kind === 'url' ? 'url' : undefined}
          autoComplete={f.kind === 'secret' ? 'off' : f.kind === 'url' ? 'url' : 'off'}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          autoFocus={i === 0}
          required={f.required}
          placeholder={f.placeholder}
          help={f.help}
          value={values[f.name] ?? ''}
          onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
        />
      ))}
      <Notice>{save.error && errorMessage(save.error)}</Notice>
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" onClick={() => onDone()}>
          Cancel
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending && <LoaderCircle className="animate-spin" />}
          {save.isPending ? 'Checking…' : link ? 'Re-link' : 'Link'}
        </Button>
      </div>
    </form>
  )
}

/** Lets everyone on the server search and queue from one of your links. */
function ShareSwitch({ link }: { link: ServiceLink }) {
  const queryClient = useQueryClient()
  const share = useMutation({
    mutationFn: (shared: boolean) => unwrap(api.PATCH('/links/{id}', { params: { path: { id: link.id } }, body: { shared } })),
    onMutate: (shared) => {
      queryClient.setQueryData(linksQuery.queryKey, (ls) => ls?.map((l) => (l.id === link.id ? { ...l, shared } : l)))
    },
    onSuccess: (saved) => {
      queryClient.setQueryData(linksQuery.queryKey, (ls) => ls?.map((l) => (l.id === saved.id ? saved : l)))
    },
    onError: () => void queryClient.invalidateQueries({ queryKey: linksQuery.queryKey, exact: true }),
    onSettled: () => void queryClient.invalidateQueries({ queryKey: usableLinksQuery.queryKey }),
  })

  return (
    <div className="mt-3 border-t border-foreground/8 pt-3">
      <label className="flex cursor-pointer items-center justify-between gap-3">
        <span className="min-w-0">
          <span className="flex items-center gap-1.5 text-sm font-medium">
            <Users className="size-4 text-muted-foreground" />
            Share with everyone here
          </span>
          <span className="block text-caption text-muted-foreground">
            {link.shared
              ? 'Everyone on this server can search it and add songs from it.'
              : 'Let others search it and add songs from it. They never see your login.'}
          </span>
        </span>
        <Switch checked={link.shared} onChange={(on) => share.mutate(on)} label={`Share ${link.accountLabel} with everyone`} />
      </label>
      <Notice className="mt-2">{share.error && errorMessage(share.error)}</Notice>
    </div>
  )
}

type Pairing = components['schemas']['Pairing']

/**
 * Links (or with `link`, re-links) a provider that pairs a device: shows a
 * code to approve on any device, waits for it, then finishes linking, or
 * for `oauth2` providers sends the browser on to sign in.
 */
function PairingPanel({
  provider,
  link,
  onDone,
}: {
  provider: Provider
  link?: ServiceLink
  onDone: (link?: ServiceLink) => void
}) {
  const queryClient = useQueryClient()
  const oauth = useBeginOAuth()
  const twoSteps = provider.linkMethod === 'oauth2'

  const begin = useMutation({
    mutationFn: () =>
      unwrap(api.POST('/pairings', { body: link ? { linkId: link.id } : { provider: provider.id } })),
  })
  const { mutate: start } = begin
  // Once per panel, even when React runs effects twice in development.
  const started = useRef(false)
  useEffect(() => {
    if (started.current) return
    started.current = true
    start()
  }, [start])
  const pairing: Pairing | undefined = begin.data

  const status = useQuery({
    queryKey: ['pairing', pairing?.id],
    queryFn: () => unwrap(api.GET('/pairings/{id}', { params: { path: { id: pairing!.id } } })),
    enabled: !!pairing,
    retry: false,
    refetchInterval: (q) => (q.state.data?.status === 'pending' || !q.state.data ? (pairing?.interval ?? 5) * 1000 : false),
  })
  const state = status.data?.status

  useEffect(() => {
    const linked = status.data?.link
    if (state !== 'linked' || !linked) return
    queryClient.setQueryData(linksQuery.queryKey, (ls = []) =>
      ls.some((l) => l.id === linked.id) ? ls.map((l) => (l.id === linked.id ? linked : l)) : [...ls, linked],
    )
    onDone(linked)
  }, [state, status.data, queryClient, onDone])

  const error = begin.error ?? status.error ?? oauth.error
  const verifyHost = pairing ? new URL(pairing.verifyUrl).host.replace(/^www\./, '') : ''

  return (
    <div className="flex flex-col gap-4">
      {error ? (
        <Notice>{errorMessage(error)}</Notice>
      ) : state === 'approved' ? (
        <div className="flex flex-col gap-2">
          <p className="flex items-center gap-2 text-sm font-medium">
            <Check className="size-4 text-success" />
            Approved
          </p>
          <p className="text-sm text-muted-foreground">
            {twoSteps && 'Step 2 of 2: '}Sign in to {provider.name} with the same account to finish.
          </p>
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <p className="text-sm text-muted-foreground">
            {twoSteps && 'Step 1 of 2: '}Open {verifyHost || 'the link'} on any device signed in to {provider.name}, and
            approve this code.
          </p>
          {pairing ? (
            <p className="font-mono text-3xl font-semibold tracking-[0.2em]" aria-live="polite">
              {pairing.userCode}
            </p>
          ) : (
            <Skeleton className="h-9 w-40 rounded-xl" />
          )}
          {pairing && (
            <p className="flex items-center gap-2 text-caption text-muted-foreground">
              <LoaderCircle className="size-3 animate-spin" />
              Waiting for you to approve it…
            </p>
          )}
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" onClick={() => onDone()}>
          Cancel
        </Button>
        {error ? (
          <Button onClick={() => start()} disabled={begin.isPending}>
            <RefreshCw data-icon="inline-start" />
            Try again
          </Button>
        ) : state === 'approved' ? (
          <Button onClick={() => oauth.mutate({ pairingId: pairing!.id })} disabled={oauth.isPending}>
            {oauth.isPending ? <LoaderCircle className="animate-spin" /> : <ExternalLink data-icon="inline-start" />}
            Continue to {provider.name}
          </Button>
        ) : (
          pairing && (
            <Button asChild>
              <a href={pairing.verifyUrl} target="_blank" rel="noreferrer">
                <ExternalLink data-icon="inline-start" />
                Open {verifyHost}
              </a>
            </Button>
          )
        )}
      </div>
    </div>
  )
}

/** Links other people shared: you can search and queue from them too. */
function SharedWithYou({ providers }: { providers: Provider[] }) {
  const me = useMe()
  const usable = useQuery(usableLinksQuery)
  const users = useQuery(usersQuery)
  const shared = usable.data?.filter((l) => l.ownerId !== me.id) ?? []
  if (shared.length === 0) return null
  return (
    <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-5">
      <h2 className="text-headline">Shared with you</h2>
      <p className="text-sm text-muted-foreground">Search and add songs from these as if they were yours.</p>
      <ul className="mt-4 flex flex-col gap-2">
        {shared.map((l) => {
          const p = providers.find((p) => p.id === l.provider)
          const owner = users.data?.find((u) => u.id === l.ownerId)
          return (
            <li key={l.id} className="flex items-center gap-3 rounded-2xl bg-muted/60 p-3">
              <ProviderIcon icon={p?.icon ?? l.provider} className="size-9 rounded-xl" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{sourceName(p?.name ?? l.provider, owner?.displayName, false)}</p>
                <p className="flex items-center gap-1.5 truncate text-caption text-muted-foreground">
                  {owner && <UserAvatar user={owner} className="size-4 text-[0.5rem]" />}
                  Shared by {owner?.displayName ?? 'someone'}
                  {l.status !== 'ok' && ` · ${statusText[l.status].toLowerCase()}`}
                </p>
              </div>
            </li>
          )
        })}
      </ul>
    </motion.section>
  )
}

/** Links to services this server no longer offers; all you can do is unlink. */
function OrphanLinks({ links }: { links: ServiceLink[] }) {
  if (links.length === 0) return null
  return (
    <motion.section variants={fadeUp} className="glass flex flex-col gap-2 rounded-3xl p-5">
      <h2 className="text-headline">No longer available</h2>
      <p className="text-sm text-muted-foreground">This server stopped offering these services.</p>
      <ul className="mt-2 flex flex-col gap-2">
        {links.map((l) => (
          <LinkRow key={l.id} link={l} highlighted={false} />
        ))}
      </ul>
    </motion.section>
  )
}

/** Sends the browser to an OAuth2 provider to link or re-link. */
function useBeginOAuth() {
  return useMutation({
    mutationFn: (body: components['schemas']['BeginOAuthLinkRequest']) => unwrap(api.POST('/links/oauth', { body })),
    onSuccess: ({ authUrl }) => window.location.assign(authUrl),
  })
}

function Expand({ children }: { children: React.ReactNode }) {
  return (
    <motion.div
      initial={{ opacity: 0, height: 0 }}
      animate={{ opacity: 1, height: 'auto' }}
      exit={{ opacity: 0, height: 0 }}
      transition={{ duration: 0.35, ease: easeOutExpo }}
      className="-mx-1 overflow-hidden px-1"
    >
      <div className="pt-4 pb-1">{children}</div>
    </motion.div>
  )
}
