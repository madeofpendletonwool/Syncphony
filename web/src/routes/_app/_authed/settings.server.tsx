import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { ArchiveRestore, AudioLines, DatabaseBackup, DoorOpen, Download, History, LoaderCircle, LogOut, Speaker, Trash2, X } from 'lucide-react'
import { motion } from 'motion/react'
import { useState, type ReactNode } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { ConfirmButton } from '@/components/confirm-button'
import { Notice } from '@/components/notice'
import { PageHeader } from '@/components/page-header'
import { ProviderIcon } from '@/components/provider-icon'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { joinAsAdmin, visibility } from '@/lib/access'
import { describeDevice } from '@/lib/account'
import {
  describeKeeping,
  describeSchedule,
  downloadURL,
  formatHour,
  frequencies,
  kindLabels,
  weekdays,
  type BackupSchedule,
} from '@/lib/backups'
import { meQuery } from '@/lib/auth'
import { roomsQuery as myRoomsQuery } from '@/lib/room'
import { fadeUp, stagger } from '@/lib/motion'
import { formatBytes, serverSettingsQuery } from '@/lib/server'
import { providersQuery } from '@/lib/services'
import { relativeTime } from '@/lib/time'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'

type User = components['schemas']['User']
type ServerSettings = components['schemas']['ServerSettings']
type RoomTaste = components['schemas']['RoomTaste']

export const Route = createFileRoute('/_app/_authed/settings/server')({
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.ensureQueryData(meQuery)
    if (me?.role !== 'admin') throw redirect({ to: '/me', replace: true })
  },
  component: Server,
})

const infoQuery = { queryKey: ['admin', 'server'], queryFn: () => unwrap(api.GET('/admin/server')) }
// The next backup and the last one's result change on their own; check back.
const backupsQuery = { queryKey: ['admin', 'backups'], queryFn: () => unwrap(api.GET('/admin/backups')), refetchInterval: 60_000 }
// Health changes as people use the app; check back while the page is open.
const live = { refetchInterval: 15_000 }
const linksQuery = { queryKey: ['admin', 'links'], queryFn: () => unwrap(api.GET('/admin/links')), ...live }
const roomsQuery = { queryKey: ['admin', 'rooms'], queryFn: () => unwrap(api.GET('/admin/rooms')), ...live }
const sessionsQuery = { queryKey: ['admin', 'sessions'], queryFn: () => unwrap(api.GET('/admin/sessions')), ...live }

function Server() {
  return (
    <>
      <PageHeader title="Server" subtitle="How this Syncphony is doing, and its settings." />
      <motion.div variants={stagger} initial="hidden" animate="show" className="flex flex-col gap-4">
        <About />
        <Backups />
        <Settings />
        <Services />
        <Rooms />
        <Devices />
      </motion.div>
    </>
  )
}

function Card({ title, hint, children }: { title: ReactNode; hint?: ReactNode; children: ReactNode }) {
  return (
    <motion.section variants={fadeUp} className="glass flex flex-col rounded-3xl p-5">
      <h2 className="text-headline">{title}</h2>
      {hint && <p className="mt-1 text-sm text-muted-foreground">{hint}</p>}
      {children}
    </motion.section>
  )
}

function Loading() {
  return (
    <div className="mt-4 flex flex-col gap-3">
      <Skeleton className="h-12 rounded-2xl" />
      <Skeleton className="h-12 rounded-2xl" />
    </div>
  )
}

/** A label and its value, in a list of facts. */
function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="min-w-0 truncate text-right text-sm font-medium">{children}</dd>
    </div>
  )
}

// --- About ----------------------------------------------------------------------

function About() {
  const info = useQuery(infoQuery)
  const last = info.data?.backups[0]

  return (
    <Card title="This server">
      {info.isPending ? (
        <Loading />
      ) : info.isError ? (
        <Notice className="mt-4">{errorMessage(info.error)}</Notice>
      ) : (
        <dl className="mt-2 flex flex-col divide-y divide-border">
          <Fact label="Version">
            <span className="font-mono text-[0.8rem]">{info.data.version}</span>
          </Fact>
          <Fact label="Running since">
            <span title={new Date(info.data.startedAt).toLocaleString()}>{relativeTime(info.data.startedAt)}</span>
          </Fact>
          <Fact label="Database">{formatBytes(info.data.databaseBytes)}</Fact>
          <Fact label="Last backup">
            {last ? (
              <span title={new Date(last.createdAt).toLocaleString()}>
                {relativeTime(last.createdAt)} · {formatBytes(last.bytes)}
              </span>
            ) : (
              <span className="text-muted-foreground">Never</span>
            )}
          </Fact>
        </dl>
      )}
    </Card>
  )
}

// --- Backups --------------------------------------------------------------------

/** How many backups show before "Show all". */
const backupsShown = 6

function Backups() {
  const queryClient = useQueryClient()
  const status = useQuery(backupsQuery)
  const [showAll, setShowAll] = useState(false)
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: backupsQuery.queryKey })
    void queryClient.invalidateQueries({ queryKey: infoQuery.queryKey })
  }
  const backup = useMutation({
    mutationFn: () => unwrap(api.POST('/admin/backups')),
    onSuccess: (b) => {
      refresh()
      toast({ message: `Backed up (${formatBytes(b.bytes)}).` })
    },
  })
  const remove = useMutation({
    mutationFn: (name: string) => unwrap(api.DELETE('/admin/backups/{name}', { params: { path: { name } } })),
    onSuccess: refresh,
  })
  const restore = useMutation({
    mutationFn: (name: string) => unwrap(api.PUT('/admin/restore', { body: { name } })),
    onSuccess: refresh,
  })
  const cancel = useMutation({
    mutationFn: () => unwrap(api.DELETE('/admin/restore')),
    onSuccess: refresh,
  })
  const error = backup.error ?? remove.error ?? restore.error ?? cancel.error

  return (
    <Card title="Backups" hint="Copies of the database, made on a schedule. Each one is checked before it's kept.">
      {status.isPending ? (
        <Loading />
      ) : status.isError ? (
        <Notice className="mt-4">{errorMessage(status.error)}</Notice>
      ) : (
        <>
          {status.data.problem && <Notice className="mt-4">{status.data.problem}</Notice>}
          {status.data.pendingRestore && (
            <PendingRestore restore={status.data.pendingRestore} onCancel={() => cancel.mutate()} cancelling={cancel.isPending} />
          )}
          <dl className="mt-2 flex flex-col divide-y divide-border">
            <Fact label="Folder">
              <span className="font-mono text-[0.8rem]" title="SYNCPHONY_BACKUP_DIR">
                {status.data.dir}
              </span>
            </Fact>
            <Fact label="Next backup">
              {status.data.nextAt ? (
                <span title={new Date(status.data.nextAt).toLocaleString()}>{relativeTime(status.data.nextAt)}</span>
              ) : (
                <span className="text-muted-foreground">None scheduled</span>
              )}
            </Fact>
          </dl>
          {status.data.lastRun?.error && (
            <Notice className="mt-2">
              The last scheduled backup failed ({relativeTime(status.data.lastRun.at)}): {status.data.lastRun.error}. It&apos;s tried
              again in 15 minutes.
            </Notice>
          )}
          <ScheduleEditor schedule={status.data.schedule} timeZone={status.data.timeZone} />
          {status.data.backups.length === 0 ? (
            <p className="mt-5 text-sm text-muted-foreground">No backups yet.</p>
          ) : (
            <ul className="mt-5 flex flex-col divide-y divide-border">
              {(showAll ? status.data.backups : status.data.backups.slice(0, backupsShown)).map((b) => (
                <li key={b.name} className="flex items-center gap-2 py-2">
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium" title={b.name}>
                      {new Date(b.createdAt).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}
                    </p>
                    <p className="truncate text-caption text-muted-foreground">
                      {kindLabels[b.kind]} · {formatBytes(b.bytes)} · {relativeTime(b.createdAt)}
                    </p>
                  </div>
                  <Button asChild variant="ghost" size="icon-sm" title="Download">
                    <a href={downloadURL(b.name)} download={b.name} aria-label={`Download the backup from ${b.createdAt}`}>
                      <Download />
                    </a>
                  </Button>
                  <ConfirmButton
                    label="Restore"
                    confirmLabel="Restore on restart?"
                    icon={<ArchiveRestore data-icon="inline-start" />}
                    compact
                    pending={restore.isPending && restore.variables === b.name}
                    disabled={status.data.pendingRestore?.from === b.name}
                    onConfirm={() => restore.mutate(b.name)}
                  />
                  <ConfirmButton
                    label="Delete"
                    confirmLabel="Delete?"
                    icon={<Trash2 data-icon="inline-start" />}
                    compact
                    pending={remove.isPending && remove.variables === b.name}
                    onConfirm={() => remove.mutate(b.name)}
                    className="hover:text-destructive"
                  />
                </li>
              ))}
            </ul>
          )}
          {status.data.backups.length > backupsShown && (
            <Button variant="ghost" size="sm" className="self-start" onClick={() => setShowAll(!showAll)}>
              {showAll ? 'Show fewer' : `Show all ${status.data.backups.length}`}
            </Button>
          )}
          <Notice className="mt-3">{error && errorMessage(error)}</Notice>
          <p className="mt-3 text-caption text-muted-foreground">
            Linked services&apos; passwords and tokens stay encrypted in backups. Restoring one needs this server&apos;s vault key (ID{' '}
            <span className="font-mono">{status.data.vaultKeyId}</span>), so keep a copy of the key somewhere safe, away from the
            backups.
          </p>
          <Button variant="secondary" className="mt-4 self-start" onClick={() => backup.mutate()} disabled={backup.isPending}>
            {backup.isPending ? <LoaderCircle className="animate-spin" /> : <DatabaseBackup data-icon="inline-start" />}
            Back up now
          </Button>
        </>
      )}
    </Card>
  )
}

function PendingRestore({
  restore,
  onCancel,
  cancelling,
}: {
  restore: components['schemas']['PendingRestore']
  onCancel: () => void
  cancelling: boolean
}) {
  return (
    <div className="mt-4 flex flex-col gap-2 rounded-2xl bg-primary/10 p-4 text-sm">
      <p className="font-medium">A backup will be restored when the server restarts</p>
      <p className="text-muted-foreground">
        <span className="font-mono text-[0.8rem]">{restore.from}</span>, with {restore.users}{' '}
        {restore.users === 1 ? 'person' : 'people'} and {restore.links} linked {restore.links === 1 ? 'service' : 'services'}.
        Restart the server to finish (for example <span className="font-mono">docker compose restart syncphony</span>). The
        database it replaces is backed up first.
      </p>
      {!restore.keyMatches && (
        <Notice>
          Its linked services were sealed with a different vault key ({restore.keyIds.join(', ')}). Set that key before restarting,
          or they&apos;ll need linking again.
        </Notice>
      )}
      <Button variant="ghost" size="sm" className="self-start" onClick={onCancel} disabled={cancelling}>
        {cancelling ? <LoaderCircle className="animate-spin" /> : <X data-icon="inline-start" />}
        Don&apos;t restore
      </Button>
    </div>
  )
}

/** A whole number field, saved when you leave it. */
function KeepField({ id, label, value, max, onSave }: { id: string; label: string; value: number; max: number; onSave: (n: number) => void }) {
  const [draft, setDraft] = useState(String(value))
  const save = () => {
    const n = Math.min(max, Math.max(0, Math.round(Number(draft))))
    if (Number.isFinite(n) && n !== value) onSave(n)
    setDraft(String(Number.isFinite(n) ? n : value))
  }
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-caption text-muted-foreground">
        {label}
      </label>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        min={0}
        max={max}
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
        className="h-9 w-20"
      />
    </div>
  )
}

function ScheduleEditor({ schedule, timeZone }: { schedule: BackupSchedule; timeZone: string }) {
  const queryClient = useQueryClient()
  const update = useMutation({
    mutationFn: (body: BackupSchedule) => unwrap(api.PUT('/admin/backups/schedule', { body })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: backupsQuery.queryKey }),
  })
  const set = (change: Partial<BackupSchedule>) => update.mutate({ ...schedule, ...change })
  const s = update.isPending && update.variables ? update.variables : schedule

  return (
    <div className="mt-4 flex flex-col gap-4">
      <div className="flex flex-col gap-2">
        <div>
          <p className="text-sm font-medium">Back up</p>
          <p className="text-caption text-muted-foreground">{describeSchedule(s)}</p>
        </div>
        <ToggleGroup
          type="single"
          value={s.frequency}
          onValueChange={(v) => v && set({ frequency: v as BackupSchedule['frequency'] })}
          aria-label="How often to back up"
          className="flex-wrap"
        >
          {frequencies.map((f) => (
            <ToggleGroupItem key={f.value} value={f.value}>
              {f.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
      {s.frequency === 'weekly' && (
        <ToggleGroup
          type="single"
          value={String(s.weekday)}
          onValueChange={(v) => v && set({ weekday: Number(v) })}
          aria-label="Day of the week"
          className="flex-wrap"
        >
          {weekdays.map((d, i) => (
            <ToggleGroupItem key={d} value={String(i)}>
              {d}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      )}
      {s.frequency !== 'off' && (
        <div className="flex flex-col gap-1">
          <label htmlFor="backup-hour" className="text-sm font-medium">
            {s.frequency === '6h' || s.frequency === '12h' ? 'Starting at' : 'At'}
          </label>
          <select
            id="backup-hour"
            value={s.hour}
            onChange={(e) => set({ hour: Number(e.target.value) })}
            className="h-9 w-32 rounded-xl border border-input bg-muted/60 px-3 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/40"
          >
            {Array.from({ length: 24 }, (_, h) => (
              <option key={h} value={h}>
                {formatHour(h)}
              </option>
            ))}
          </select>
          <p className="text-caption text-muted-foreground">Server time ({timeZone}). Set TZ on the server to change it.</p>
        </div>
      )}
      <div className="flex flex-col gap-2">
        <p className="text-sm font-medium">Keep</p>
        <div className="flex flex-wrap gap-4">
          <KeepField id="keep-daily" label="Days" value={schedule.keepDaily} max={90} onSave={(keepDaily) => set({ keepDaily })} />
          <KeepField id="keep-weekly" label="Weeks" value={schedule.keepWeekly} max={52} onSave={(keepWeekly) => set({ keepWeekly })} />
          <KeepField id="keep-monthly" label="Months" value={schedule.keepMonthly} max={36} onSave={(keepMonthly) => set({ keepMonthly })} />
        </div>
        <p className="text-caption text-muted-foreground">
          {describeKeeping(s)} The newest 10 made by hand, and 3 each from before upgrades and restores, are kept too.
        </p>
      </div>
      <Notice>{update.error && errorMessage(update.error)}</Notice>
    </div>
  )
}

// --- Settings -------------------------------------------------------------------

const inviteExpiries = [
  { hours: 24, label: '1 day' },
  { hours: 72, label: '3 days' },
  { hours: 168, label: '1 week' },
  { hours: 720, label: '30 days' },
]

function Settings() {
  const queryClient = useQueryClient()
  const settings = useQuery(serverSettingsQuery)
  const update = useMutation({
    mutationFn: (body: components['schemas']['ServerSettingsUpdate']) => unwrap(api.PATCH('/server-settings', { body })),
    onSuccess: (st) => queryClient.setQueryData(serverSettingsQuery.queryKey, st),
  })

  return (
    <Card title="Settings" hint="For the whole server, not one room.">
      {settings.isPending ? (
        <Loading />
      ) : settings.isError ? (
        <Notice className="mt-4">{errorMessage(settings.error)}</Notice>
      ) : (
        <div className="mt-4 flex flex-col gap-5">
          <InstanceName settings={settings.data} onSave={(instanceName) => update.mutate({ instanceName })} />
          <div className="flex flex-col gap-2">
            <div>
              <p className="text-sm font-medium">New invites last</p>
              <p className="text-caption text-muted-foreground">What the People page picks unless you choose otherwise</p>
            </div>
            <ToggleGroup
              type="single"
              value={String(settings.data.inviteExpiryHours)}
              onValueChange={(v) => v && update.mutate({ inviteExpiryHours: Number(v) })}
              aria-label="New invites last"
              className="flex-wrap"
            >
              {(inviteExpiries.some((e) => e.hours === settings.data.inviteExpiryHours)
                ? inviteExpiries
                : [...inviteExpiries, { hours: settings.data.inviteExpiryHours, label: `${settings.data.inviteExpiryHours} hours` }]
              ).map((e) => (
                <ToggleGroupItem key={e.hours} value={String(e.hours)}>
                  {e.label}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </div>
          <Notice>{update.error && errorMessage(update.error)}</Notice>
        </div>
      )}
    </Card>
  )
}

/** The server's name, saved on Enter or when you leave the field. */
function InstanceName({ settings, onSave }: { settings: ServerSettings; onSave: (name: string) => void }) {
  const [draft, setDraft] = useState(settings.instanceName)
  const save = () => {
    const next = draft.trim()
    if (next !== settings.instanceName) onSave(next)
    setDraft(next)
  }
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor="instance-name" className="text-sm font-medium">
        Server name
      </label>
      <Input
        id="instance-name"
        value={draft}
        maxLength={40}
        placeholder="Syncphony"
        autoComplete="off"
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
      />
      <p className="text-caption text-muted-foreground">Shown in the browser tab and on everyone&apos;s Me page, like &ldquo;The Den&rdquo;.</p>
    </div>
  )
}

// --- Services -------------------------------------------------------------------

function Services() {
  const links = useQuery(linksQuery)
  const providers = useQuery(providersQuery)
  const users = useQuery(usersQuery)
  const userById = (id: string) => users.data?.find((u) => u.id === id)
  const failing = links.data?.filter((l) => l.status !== 'ok').length ?? 0

  return (
    <Card
      title={
        <>
          Linked services
          {failing > 0 && (
            <Badge variant="destructive" className="ml-2 align-middle">
              {failing === 1 ? '1 needs attention' : `${failing} need attention`}
            </Badge>
          )}
        </>
      }
      hint="Everyone's, and whether they're working. Only their owner can link one again."
    >
      {links.isPending ? (
        <Loading />
      ) : links.isError ? (
        <Notice className="mt-4">{errorMessage(links.error)}</Notice>
      ) : links.data.length === 0 ? (
        <p className="mt-4 text-sm text-muted-foreground">Nobody has linked a service yet.</p>
      ) : (
        <ul className="mt-3 flex flex-col">
          {links.data.map((l) => {
            const p = providers.data?.find((x) => x.id === l.provider)
            const owner = userById(l.ownerId)
            return (
              <li key={l.id} className="flex items-center gap-3 py-2">
                <ProviderIcon icon={p?.icon ?? l.provider} className="size-9 rounded-xl" />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">
                    {p?.name ?? l.provider} <span className="font-normal text-muted-foreground">· {l.accountLabel}</span>
                  </p>
                  <p className="truncate text-caption text-muted-foreground">
                    {owner?.displayName ?? 'Someone'}
                    {l.shared && ' · shared'}
                    {l.status === 'ok'
                      ? l.lastOkAt && ` · worked ${relativeTime(l.lastOkAt)}`
                      : (l.statusDetail && ` · ${l.statusDetail}`) || (l.lastOkAt && ` · last worked ${relativeTime(l.lastOkAt)}`)}
                  </p>
                </div>
                <LinkStatus status={l.status} />
              </li>
            )
          })}
        </ul>
      )}
    </Card>
  )
}

function LinkStatus({ status }: { status: components['schemas']['ServiceLink']['status'] }) {
  if (status === 'ok') return <Badge variant="outline">Working</Badge>
  return <Badge variant="destructive">{status === 'needs_relink' ? 'Needs relinking' : 'Failing'}</Badge>
}

// --- Rooms ----------------------------------------------------------------------

const stateLabels: Record<components['schemas']['PlaybackState'], string> = {
  idle: 'Quiet',
  loading: 'Starting',
  playing: 'Playing',
  paused: 'Paused',
}

function Rooms() {
  const [tasteOf, setTasteOf] = useState<string | null>(null)
  const rooms = useQuery(roomsQuery)
  const users = useQuery(usersQuery)
  const userById = (id: string) => users.data?.find((u) => u.id === id)
  const queryClient = useQueryClient()
  const join = useMutation({
    mutationFn: (roomId: string) => joinAsAdmin(roomId),
    onSuccess: (room) => {
      void queryClient.invalidateQueries({ queryKey: roomsQuery.queryKey })
      void queryClient.invalidateQueries({ queryKey: myRoomsQuery.queryKey })
      toast({ message: `You're in ${room.name}. Everyone there was told.` })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  return (
    <Card
      title="Rooms"
      hint="Who has each room open, and the speaker it plays through. You can change or delete any room; to see inside one that isn't open to you, join it as an admin, and its members are told."
    >
      {rooms.isPending ? (
        <Loading />
      ) : rooms.isError ? (
        <Notice className="mt-4">{errorMessage(rooms.error)}</Notice>
      ) : rooms.data.length === 0 ? (
        <p className="mt-4 text-sm text-muted-foreground">No rooms yet.</p>
      ) : (
        <ul className="mt-3 flex flex-col divide-y divide-border">
          {rooms.data.map((r) => {
            const members = r.members.map(userById).filter((u): u is User => !!u)
            const v = visibility(r.visibility)
            return (
              <li key={r.roomId} className="flex flex-col gap-1.5 py-3">
                <div className="flex items-center gap-2">
                  <p className="min-w-0 flex-1 truncate font-medium">{r.roomName}</p>
                  {r.visibility !== 'open' && (
                    <Badge variant="outline" className="gap-1" title={v.hint}>
                      <v.icon className="size-3" />
                      {v.label}
                    </Badge>
                  )}
                  <Badge variant={r.state === 'playing' ? 'default' : 'outline'}>{stateLabels[r.state]}</Badge>
                  {r.canEnter && (
                    <Button
                      size="xs"
                      variant="ghost"
                      aria-expanded={tasteOf === r.roomId}
                      onClick={() => setTasteOf(tasteOf === r.roomId ? null : r.roomId)}
                    >
                      <AudioLines data-icon="inline-start" />
                      DJ's taste
                    </Button>
                  )}
                  {!r.canEnter && (
                    <Button size="xs" variant="ghost" disabled={join.isPending} onClick={() => join.mutate(r.roomId)}>
                      {join.isPending && join.variables === r.roomId ? <LoaderCircle className="animate-spin" /> : <DoorOpen data-icon="inline-start" />}
                      Join as admin
                    </Button>
                  )}
                </div>
                {r.title && <p className="truncate text-sm text-muted-foreground">{r.title}</p>}
                <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-caption text-muted-foreground">
                  <span className="flex items-center gap-1.5">
                    {members.length > 0 ? (
                      <>
                        <span className="flex -space-x-1.5">
                          {members.slice(0, 5).map((u) => (
                            <UserAvatar key={u.id} user={u} className="size-5 text-[0.5rem] ring-2 ring-background" />
                          ))}
                        </span>
                        {members.length === 1 ? members[0].displayName : `${members.length} here`}
                      </>
                    ) : (
                      'Nobody here'
                    )}
                  </span>
                  <span className="flex items-center gap-1">
                    <Speaker className="size-3.5" />
                    {r.player
                      ? `${r.player.name || 'A device'}${userById(r.player.userId) ? ` (${userById(r.player.userId)!.displayName})` : ''}, seen ${relativeTime(r.player.lastSeen)}`
                      : 'No speaker'}
                  </span>
                  <span>Owner: {userById(r.ownerId)?.displayName ?? 'someone'}</span>
                </div>
                {tasteOf === r.roomId && <Taste roomId={r.roomId} />}
              </li>
            )
          })}
        </ul>
      )}
    </Card>
  )
}

/** What the DJ has learned of a room's taste, to check its learning makes sense. */
function Taste({ roomId }: { roomId: string }) {
  const taste = useQuery({
    queryKey: ['admin', 'rooms', roomId, 'taste'],
    queryFn: () => unwrap(api.GET('/admin/rooms/{roomId}/taste', { params: { path: { roomId } } })),
  })
  if (taste.isPending) return <Loading />
  if (taste.isError) return <Notice className="mt-2">{errorMessage(taste.error)}</Notice>
  const t: RoomTaste = taste.data
  return (
    <div className="mt-2 grid gap-4 rounded-2xl bg-muted/40 p-4 sm:grid-cols-2">
      <section className="flex flex-col gap-2">
        <h3 className="flex items-center gap-2 text-sm font-medium">
          Tonight
          {t.shifted && <Badge variant="outline">Vibe changing</Badge>}
        </h3>
        {t.tonight.length === 0 ? (
          <p className="text-caption text-muted-foreground">Nothing yet.</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {t.tonight.map((a) => (
              <WeightRow key={a.name} name={a.name} weight={a.weight} note={`${a.plays} ${a.plays === 1 ? 'play' : 'plays'}`} />
            ))}
          </ul>
        )}
        {t.avoided.length > 0 && <p className="text-caption text-muted-foreground">Turned away: {t.avoided.join(', ')}</p>}
      </section>
      <section className="flex flex-col gap-2">
        <h3 className="flex items-center gap-1.5 text-sm font-medium">
          <History className="size-3.5" />
          Past {t.nights === 1 ? 'night' : `${t.nights} nights`}
        </h3>
        {t.artists.length === 0 ? (
          <p className="text-caption text-muted-foreground">No nights have ended yet.</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {t.artists.map((a) => (
              <WeightRow
                key={a.name}
                name={a.name}
                weight={a.weight}
                note={[
                  a.lovedAt ? (a.nightsAgo === 0 ? 'last night' : `${a.nightsAgo} nights ago`) : 'never liked',
                  a.veto > 0 ? `veto ${Math.round(a.veto * 100)}%` : '',
                ]
                  .filter(Boolean)
                  .join(' · ')}
                badge={a.throwback ? 'Throwback' : undefined}
              />
            ))}
          </ul>
        )}
        {t.tags.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {t.tags.map((g) => (
              <Badge key={g.name} variant="outline" style={{ opacity: 0.4 + 0.6 * g.weight }}>
                {g.name}
              </Badge>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}

function WeightRow({ name, weight, note, badge }: { name: string; weight: number; note: string; badge?: string }) {
  return (
    <li className="flex flex-col gap-0.5">
      <div className="flex items-baseline gap-2 text-sm">
        <span className="min-w-0 flex-1 truncate">{name}</span>
        {badge && <Badge variant="outline">{badge}</Badge>}
        <span className="shrink-0 text-caption text-muted-foreground">{note}</span>
      </div>
      <div className="h-1 overflow-hidden rounded-full bg-border">
        <div className="h-full rounded-full bg-primary" style={{ width: `${Math.round(weight * 100)}%` }} />
      </div>
    </li>
  )
}

// --- Devices --------------------------------------------------------------------

function Devices() {
  const queryClient = useQueryClient()
  const sessions = useQuery(sessionsQuery)
  const users = useQuery(usersQuery)
  const userById = (id: string) => users.data?.find((u) => u.id === id)
  const revoke = useMutation({
    mutationFn: (id: string) => unwrap(api.DELETE('/admin/sessions/{id}', { params: { path: { id } } })),
    onSuccess: (_, id) =>
      queryClient.setQueryData<components['schemas']['UserSession'][]>(sessionsQuery.queryKey, (list) => list?.filter((s) => s.id !== id)),
  })

  return (
    <Card title="Signed-in devices" hint="Everyone's, most recently used first. Guests aren't listed.">
      {sessions.isPending ? (
        <Loading />
      ) : sessions.isError ? (
        <Notice className="mt-4">{errorMessage(sessions.error)}</Notice>
      ) : (
        <>
          <ul className="mt-3 flex flex-col">
            {sessions.data.map((s) => {
              const u = userById(s.userId)
              return (
                <li key={s.id} className="flex items-center gap-3 py-2">
                  {u && <UserAvatar user={u} className="size-9" />}
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium">
                      {u?.displayName ?? 'Someone'}
                      <span className="font-normal text-muted-foreground"> · {describeDevice(s.userAgent).label}</span>
                    </p>
                    <p className="truncate text-caption text-muted-foreground">
                      {s.current ? 'This device' : `Used ${relativeTime(s.lastSeenAt)}`} · signed in {relativeTime(s.createdAt)}
                    </p>
                  </div>
                  {!s.current && (
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Sign ${u?.displayName ?? 'them'} out of this device`}
                      title="Sign out"
                      onClick={() => revoke.mutate(s.id)}
                      disabled={revoke.isPending}
                      className="hover:text-destructive"
                    >
                      {revoke.isPending && revoke.variables === s.id ? <LoaderCircle className="animate-spin" /> : <LogOut />}
                    </Button>
                  )}
                </li>
              )
            })}
          </ul>
          <Notice className="mt-2">{revoke.error && errorMessage(revoke.error)}</Notice>
        </>
      )}
    </Card>
  )
}
