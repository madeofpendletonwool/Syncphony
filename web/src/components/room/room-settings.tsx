import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRightLeft, LoaderCircle, Minus, Plus, Trash2, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import { useState, type ReactNode } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { useMe } from '@/lib/auth'
import { easeOutExpo } from '@/lib/motion'
import { roomsQuery, type Room } from '@/lib/room'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'

type Change = components['schemas']['UpdateRoomRequest']
type Fairness = Room['fairness']

// Vote thresholds: a skip passes once more than this percent have voted.
const THRESHOLDS = [
  { percent: 50, label: 'Majority' },
  { percent: 66, label: 'Two-thirds' },
  { percent: 99, label: 'Everyone' },
]

// Limits offered for songs in a row and the cooldown; 0 is off.
const LIMITS = [0, 1, 2, 3]

const REPEAT_WINDOWS = [
  { minutes: 0, label: 'Off' },
  { minutes: 60, label: '1 hour' },
  { minutes: 180, label: '3 hours' },
  { minutes: 720, label: 'All night' },
]

const MAX_WEIGHT = 4

// Songs each guest may add over the night; 0 is no limit.
const GUEST_SONGS = [5, 10, 20, 0]

/**
 * A room's name, who can control playback, and how turns work, for its
 * owner and admins. Changes save as you make them, and everyone in the
 * room sees them at once. Also where the room is handed over or deleted.
 */
export function RoomSettings({ room, open, onOpenChange }: { room: Room; open: boolean; onOpenChange: (open: boolean) => void }) {
  const queryClient = useQueryClient()
  const update = useMutation({
    mutationFn: (body: Change) => unwrap(api.PATCH('/rooms/{roomId}', { params: { path: { roomId: room.id } }, body })),
    onMutate: (body) => {
      const before = queryClient.getQueryData(roomsQuery.queryKey)
      queryClient.setQueryData(roomsQuery.queryKey, (rs) =>
        rs?.map((r) =>
          r.id === room.id
            ? {
                ...r,
                name: body.name ?? r.name,
                permissions: { ...r.permissions, ...body.permissions },
                skipVotePercent: body.skipVotePercent ?? r.skipVotePercent,
                fairnessMode: body.fairnessMode ?? r.fairnessMode,
                fairness: body.fairness ?? r.fairness,
                matching: body.matching ?? r.matching,
                autopilot: body.autopilot ?? r.autopilot,
                guests: body.guests ?? r.guests,
              }
            : r,
        ),
      )
      return { before }
    },
    onError: (err, _, ctx) => {
      if (ctx?.before) queryClient.setQueryData(roomsQuery.queryKey, ctx.before)
      toast({ message: errorMessage(err), tone: 'error' })
    },
    onSuccess: (r) => queryClient.setQueryData(roomsQuery.queryKey, (rs) => rs?.map((x) => (x.id === r.id ? r : x))),
  })
  const me = useMe()
  // "Owner" permissions mean the room's owner, who may not be you.
  const ownerLabel = room.ownerId === me.id ? 'Only you' : 'Only the owner'
  const p = room.permissions
  const set = (permissions: Change['permissions']) => update.mutate({ permissions })
  // fairness is replaced as a whole.
  const tune = (change: Partial<Fairness>) => update.mutate({ fairness: { ...room.fairness, ...change } })

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <Dialog.Portal forceMount>
            <Dialog.Overlay asChild forceMount>
              <motion.div
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                className="fixed inset-0 z-50 bg-black/40"
              />
            </Dialog.Overlay>
            <Dialog.Content asChild forceMount>
              <motion.div
                initial={{ opacity: 0, y: 24 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: 24 }}
                transition={{ duration: 0.35, ease: easeOutExpo }}
                className="glass-strong fixed inset-x-0 bottom-0 z-50 mx-auto flex max-h-[90dvh] w-full max-w-md flex-col gap-5 overflow-y-auto rounded-t-3xl p-5 pb-[calc(env(safe-area-inset-bottom)+1.25rem)] shadow-float outline-none sm:inset-x-4 sm:top-1/2 sm:bottom-auto sm:-translate-y-1/2 sm:rounded-3xl sm:pb-5"
              >
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <Dialog.Title className="text-headline">Room settings</Dialog.Title>
                    <Dialog.Description className="mt-1 text-sm text-muted-foreground">
                      Who controls playback, and how everyone takes turns.
                      {room.ownerId !== me.id && ' You can change it because you’re an admin.'}
                    </Dialog.Description>
                  </div>
                  <Dialog.Close asChild>
                    <Button size="icon-sm" variant="ghost" aria-label="Close" className="-mt-1 -mr-1">
                      <X />
                    </Button>
                  </Dialog.Close>
                </div>

                <RoomName name={room.name} onSave={(name) => update.mutate({ name })} />

                <SectionTitle hint={room.ownerId === me.id ? 'You always can, and anyone can skip their own song' : 'The owner always can, and anyone can skip their own song'}>
                  Controls
                </SectionTitle>
                <Setting label="Play and pause">
                  <Levels value={p.playPause} onChange={(playPause) => set({ playPause })} label="Who can play and pause" ownerLabel={ownerLabel} />
                </Setting>
                <Setting label="Seek" hint="Scrubbing, and restarting the song">
                  <Levels value={p.seek} onChange={(seek) => set({ seek })} label="Who can seek" ownerLabel={ownerLabel} />
                </Setting>
                <Setting label="Skip" hint={p.skip === 'vote' ? 'Everyone else votes, and the song is skipped once enough have' : undefined}>
                  <ToggleGroup
                    type="single"
                    value={p.skip}
                    onValueChange={(v) => v && set({ skip: v as Room['permissions']['skip'] })}
                    aria-label="Who can skip"
                  >
                    <ToggleGroupItem value="everyone">Everyone</ToggleGroupItem>
                    <ToggleGroupItem value="vote">Vote</ToggleGroupItem>
                    <ToggleGroupItem value="owner">{ownerLabel}</ToggleGroupItem>
                  </ToggleGroup>
                </Setting>
                <AnimatePresence initial={false}>
                  {p.skip === 'vote' && (
                    <motion.div
                      initial={{ opacity: 0, height: 0 }}
                      animate={{ opacity: 1, height: 'auto' }}
                      exit={{ opacity: 0, height: 0 }}
                      transition={{ duration: 0.25, ease: easeOutExpo }}
                      className="-mt-2 overflow-hidden"
                    >
                      <Setting label="Votes to skip" hint="Of the people in the room, not counting whoever queued the song">
                        <ToggleGroup
                          type="single"
                          value={String(room.skipVotePercent)}
                          onValueChange={(v) => v && update.mutate({ skipVotePercent: Number(v) })}
                          aria-label="Votes needed to skip"
                        >
                          {THRESHOLDS.map((t) => (
                            <ToggleGroupItem key={t.percent} value={String(t.percent)}>
                              {t.label}
                            </ToggleGroupItem>
                          ))}
                        </ToggleGroup>
                      </Setting>
                    </motion.div>
                  )}
                </AnimatePresence>
                <Setting label="Become the speaker" hint="Play the room's audio on their device">
                  <Levels value={p.speaker} onChange={(speaker) => set({ speaker })} label="Who can become the speaker" ownerLabel={ownerLabel} />
                </Setting>

                <SectionTitle hint="These only hold while someone else has songs waiting; the music never stops for them">
                  Turns
                </SectionTitle>
                <Setting
                  label="Order"
                  hint={room.fairnessMode === 'round_robin' ? 'Everyone takes turns, one song each' : 'Songs play in the order they were added'}
                >
                  <ToggleGroup
                    type="single"
                    value={room.fairnessMode}
                    onValueChange={(v) => v && update.mutate({ fairnessMode: v as Room['fairnessMode'] })}
                    aria-label="Play order"
                  >
                    <ToggleGroupItem value="round_robin">Take turns</ToggleGroupItem>
                    <ToggleGroupItem value="fifo">First come</ToggleGroupItem>
                  </ToggleGroup>
                </Setting>
                <Setting label="Most songs in a row" hint="From one person">
                  <Limit value={room.fairness.maxInARow} onChange={(maxInARow) => tune({ maxInARow })} label="Most songs in a row" />
                </Setting>
                <Setting label="Cooldown" hint="Other people's songs between one person's">
                  <Limit value={room.fairness.cooldown} onChange={(cooldown) => tune({ cooldown })} label="Cooldown" />
                </Setting>
                <Setting label="No repeats within" hint="Turns away a song that's waiting, playing, or played recently">
                  <ToggleGroup
                    type="single"
                    value={String(room.fairness.repeatWindowMinutes)}
                    onValueChange={(v) => v && tune({ repeatWindowMinutes: Number(v) })}
                    aria-label="No repeats within"
                    className="flex-wrap"
                  >
                    {REPEAT_WINDOWS.map((w) => (
                      <ToggleGroupItem key={w.minutes} value={String(w.minutes)}>
                        {w.label}
                      </ToggleGroupItem>
                    ))}
                  </ToggleGroup>
                </Setting>
                {room.fairnessMode === 'round_robin' && (
                  <Weights weights={room.fairness.weights} onChange={(weights) => tune({ weights })} />
                )}

                <SectionTitle hint="When the queue runs dry, keep the music going with songs like the ones the room has played">
                  Autopilot
                </SectionTitle>
                <Toggle
                  label="Autopilot DJ"
                  hint="Takes turns drawing on everyone's songs. Anything someone adds plays first"
                  checked={room.autopilot.on}
                  onChange={(on) => update.mutate({ autopilot: { ...room.autopilot, on } })}
                />
                <AnimatePresence initial={false}>
                  {room.autopilot.on && (
                    <motion.div
                      initial={{ opacity: 0, height: 0 }}
                      animate={{ opacity: 1, height: 'auto' }}
                      exit={{ opacity: 0, height: 0 }}
                      transition={{ duration: 0.25, ease: easeOutExpo }}
                      className="-mt-2 overflow-hidden"
                    >
                      <Setting
                        label="How adventurous"
                        hint={
                          room.autopilot.adventure === 'discovery'
                            ? 'Other artists, further afield'
                            : 'Close to what the room has played, the same artists included'
                        }
                      >
                        <ToggleGroup
                          type="single"
                          value={room.autopilot.adventure}
                          onValueChange={(v) => v && update.mutate({ autopilot: { ...room.autopilot, adventure: v as Room['autopilot']['adventure'] } })}
                          aria-label="How adventurous autopilot is"
                        >
                          <ToggleGroupItem value="similar">Similar</ToggleGroupItem>
                          <ToggleGroupItem value="discovery">Discovery</ToggleGroupItem>
                        </ToggleGroup>
                      </Setting>
                    </motion.div>
                  )}
                </AnimatePresence>

                <SectionTitle hint="Friends of friends scan a QR code and add songs, with no account. They search the server's shared services">
                  Guests
                </SectionTitle>
                <Toggle
                  label="Let guests join"
                  hint="Anyone here can show the guest QR code, and so can the big screen"
                  checked={room.guests.allowed}
                  onChange={(allowed) => update.mutate({ guests: { ...room.guests, allowed } })}
                />
                <AnimatePresence initial={false}>
                  {room.guests.allowed && (
                    <motion.div
                      initial={{ opacity: 0, height: 0 }}
                      animate={{ opacity: 1, height: 'auto' }}
                      exit={{ opacity: 0, height: 0 }}
                      transition={{ duration: 0.25, ease: easeOutExpo }}
                      className="-mt-2 flex flex-col gap-5 overflow-hidden"
                    >
                      <Setting label="Songs per guest" hint="Over their whole visit. Each guest gets their own turn">
                        <ToggleGroup
                          type="single"
                          value={String(room.guests.maxSongs)}
                          onValueChange={(v) => v && update.mutate({ guests: { ...room.guests, maxSongs: Number(v) } })}
                          aria-label="Songs per guest"
                        >
                          {(GUEST_SONGS.includes(room.guests.maxSongs) ? GUEST_SONGS : [room.guests.maxSongs, ...GUEST_SONGS]).map((n) => (
                            <ToggleGroupItem key={n} value={String(n)} className="min-w-11">
                              {n === 0 ? 'No limit' : n}
                            </ToggleGroupItem>
                          ))}
                        </ToggleGroup>
                      </Setting>
                      <Toggle
                        label="Guests can vote"
                        hint="Vote to skip, and heart songs for song of the night"
                        checked={room.guests.canVote}
                        onChange={(canVote) => update.mutate({ guests: { ...room.guests, canVote } })}
                      />
                    </motion.div>
                  )}
                </AnimatePresence>

                <SectionTitle hint="Uses the services of the people in the room, and shared ones">Other services</SectionTitle>
                <Toggle
                  label="Fill in from other services"
                  hint="When a song's service can't play it, play the same recording from someone else's"
                  checked={room.matching.fallback}
                  onChange={(fallback) => update.mutate({ matching: { ...room.matching, fallback } })}
                />
                <Toggle
                  label="Borrow songs"
                  hint="Anyone can add a song the room played again, even if it's only on someone else's service"
                  checked={room.matching.borrow}
                  onChange={(borrow) => update.mutate({ matching: { ...room.matching, borrow } })}
                />

                <SectionTitle>This room</SectionTitle>
                <ManageRoom room={room} onGone={() => onOpenChange(false)} />
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}

function SectionTitle({ children, hint }: { children: ReactNode; hint?: string }) {
  return (
    <div className="-mb-2 border-t border-border pt-4">
      <h3 className="text-caption font-semibold tracking-wide text-muted-foreground uppercase">{children}</h3>
      {hint && <p className="text-caption text-muted-foreground">{hint}</p>}
    </div>
  )
}

function Toggle({ label, hint, checked, onChange }: { label: string; hint: string; checked: boolean; onChange: (on: boolean) => void }) {
  return (
    <label className="flex cursor-pointer items-start justify-between gap-4">
      <span>
        <span className="block text-sm font-medium">{label}</span>
        <span className="block text-caption text-muted-foreground">{hint}</span>
      </span>
      <Switch checked={checked} onChange={onChange} label={label} />
    </label>
  )
}

function Limit({ value, onChange, label }: { value: number; onChange: (v: number) => void; label: string }) {
  // A limit set through the API to something not offered still shows.
  const options = LIMITS.includes(value) ? LIMITS : [...LIMITS, value].sort((a, b) => a - b)
  return (
    <ToggleGroup type="single" value={String(value)} onValueChange={(v) => v && onChange(Number(v))} aria-label={label}>
      {options.map((n) => (
        <ToggleGroupItem key={n} value={String(n)} className="min-w-11">
          {n === 0 ? 'Off' : n}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

/** Songs per turn for each person, e.g. two for whoever's birthday it is. */
function Weights({ weights, onChange }: { weights: Fairness['weights']; onChange: (w: Fairness['weights']) => void }) {
  const users = useQuery(usersQuery)
  const set = (id: string, w: number) => {
    const next = { ...weights }
    if (w <= 1) delete next[id]
    else next[id] = w
    onChange(next)
  }
  return (
    <div className="flex flex-col gap-2">
      <div>
        <p className="text-sm font-medium">Songs per turn</p>
        <p className="text-caption text-muted-foreground">Give someone more of a say, like whoever&apos;s birthday it is</p>
      </div>
      <ul className="flex flex-col gap-1">
        {(users.data ?? []).filter((u) => !u.guest && !u.removed).map((u) => {
          const w = weights[u.id] ?? 1
          return (
            <li key={u.id} className="flex items-center gap-3 rounded-2xl px-1 py-1">
              <UserAvatar user={u} className="size-7 text-[0.65rem]" />
              <span className="min-w-0 flex-1 truncate text-sm">{u.displayName}</span>
              <Button size="icon-sm" variant="ghost" aria-label={`Fewer songs per turn for ${u.displayName}`} disabled={w <= 1} onClick={() => set(u.id, w - 1)}>
                <Minus />
              </Button>
              <span className="w-4 text-center text-sm font-medium tabular-nums" aria-label={`${w} per turn`}>
                {w}
              </span>
              <Button size="icon-sm" variant="ghost" aria-label={`More songs per turn for ${u.displayName}`} disabled={w >= MAX_WEIGHT} onClick={() => set(u.id, w + 1)}>
                <Plus />
              </Button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

function Setting({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <div>
        <p className="text-sm font-medium">{label}</p>
        {hint && <p className="text-caption text-muted-foreground">{hint}</p>}
      </div>
      {children}
    </div>
  )
}

type Level = components['schemas']['PermissionLevel']

function Levels({ value, onChange, label, ownerLabel }: { value: Level; onChange: (v: Level) => void; label: string; ownerLabel: string }) {
  return (
    <ToggleGroup type="single" value={value} onValueChange={(v) => v && onChange(v as Level)} aria-label={label}>
      <ToggleGroupItem value="everyone">Everyone</ToggleGroupItem>
      <ToggleGroupItem value="owner">{ownerLabel}</ToggleGroupItem>
    </ToggleGroup>
  )
}

/** The room's name, saved when you're done typing (Enter, or leaving the field). */
function RoomName({ name, onSave }: { name: string; onSave: (name: string) => void }) {
  const [draft, setDraft] = useState(name)
  // Someone else renamed it while the sheet was open.
  const [was, setWas] = useState(name)
  if (name !== was) {
    setWas(name)
    setDraft(name)
  }
  const save = () => {
    const next = draft.trim()
    if (next && next !== name) onSave(next)
    else setDraft(name)
  }
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor="room-name" className="text-sm font-medium">
        Name
      </label>
      <Input
        id="room-name"
        value={draft}
        maxLength={64}
        autoComplete="off"
        onChange={(e) => setDraft(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => {
          if (e.key === 'Enter') e.currentTarget.blur()
          if (e.key === 'Escape' && draft !== name) {
            // Undo the edit rather than closing the sheet.
            e.stopPropagation()
            setDraft(name)
          }
        }}
      />
    </div>
  )
}

/** Hand the room to someone else, or delete it. */
function ManageRoom({ room, onGone }: { room: Room; onGone: () => void }) {
  const me = useMe()
  const queryClient = useQueryClient()
  const users = useQuery(usersQuery)
  const [open, setOpen] = useState<'transfer' | 'delete'>()
  const owner = users.data?.find((u) => u.id === room.ownerId)
  // Anyone who can sign in and look after it.
  const heirs = (users.data ?? []).filter((u) => !u.guest && !u.removed && !u.disabled && u.id !== room.ownerId)
  const path = { params: { path: { roomId: room.id } } }

  const transfer = useMutation({
    mutationFn: (userId: string) => unwrap(api.PUT('/rooms/{roomId}/owner', { ...path, body: { userId } })),
    onSuccess: (r) => {
      queryClient.setQueryData(roomsQuery.queryKey, (rs) => rs?.map((x) => (x.id === r.id ? r : x)))
      const to = users.data?.find((u) => u.id === r.ownerId)
      toast({ message: `${r.name} is ${to ? possessive(to.displayName) : 'theirs'} now.` })
      setOpen(undefined)
      // Without being an admin, it's no longer yours to change.
      if (me.role !== 'admin') onGone()
    },
  })
  const remove = useMutation({
    mutationFn: () => unwrap(api.DELETE('/rooms/{roomId}', path)),
    onSuccess: () => {
      queryClient.setQueryData(roomsQuery.queryKey, (rs) => rs?.filter((r) => r.id !== room.id))
      onGone()
    },
  })
  const toggle = (which: 'transfer' | 'delete') => {
    transfer.reset()
    remove.reset()
    setOpen((o) => (o === which ? undefined : which))
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        {owner && <UserAvatar user={owner} className="size-8 text-[0.7rem]" />}
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">Owner</p>
          <p className="truncate text-caption text-muted-foreground">
            {room.ownerId === me.id ? 'You' : (owner?.displayName ?? 'Someone')}
          </p>
        </div>
        <Button size="sm" variant="ghost" aria-expanded={open === 'transfer'} onClick={() => toggle('transfer')} disabled={heirs.length === 0}>
          <ArrowRightLeft data-icon="inline-start" />
          Hand over
        </Button>
      </div>
      <Collapse open={open === 'transfer'}>
        <div className="flex flex-col gap-1 rounded-2xl bg-muted/60 p-2">
          <p className="px-2 pt-1 pb-1.5 text-caption text-muted-foreground">
            They&apos;ll own it, and the &ldquo;only the owner&rdquo; controls go with it.
          </p>
          <ul className="flex max-h-56 flex-col gap-0.5 overflow-y-auto">
            {heirs.map((u) => (
              <li key={u.id}>
                <button
                  type="button"
                  disabled={transfer.isPending}
                  onClick={() => transfer.mutate(u.id)}
                  className="flex w-full items-center gap-3 rounded-xl px-2 py-1.5 text-left text-sm transition-colors outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
                >
                  <UserAvatar user={u} className="size-7 text-[0.65rem]" />
                  <span className="min-w-0 flex-1 truncate">{u.id === me.id ? 'You' : u.displayName}</span>
                  {transfer.isPending && transfer.variables === u.id && <LoaderCircle className="size-4 animate-spin" />}
                </button>
              </li>
            ))}
          </ul>
          <Notice className="px-2">{transfer.error && errorMessage(transfer.error)}</Notice>
        </div>
      </Collapse>

      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">Delete room</p>
          <p className="text-caption text-muted-foreground">Its queue, history and recaps go with it</p>
        </div>
        <Button size="sm" variant="ghost" aria-expanded={open === 'delete'} onClick={() => toggle('delete')} className="hover:text-destructive">
          <Trash2 data-icon="inline-start" />
          Delete
        </Button>
      </div>
      <Collapse open={open === 'delete'}>
        <div className="flex flex-col gap-3 rounded-2xl bg-destructive/10 p-3">
          <p className="text-sm">
            Delete <span className="font-medium">{room.name}</span> for everyone? Anyone in it now is sent back to the room list,
            and its guests&apos; passes stop working. This can&apos;t be undone.
          </p>
          <Notice>{remove.error && errorMessage(remove.error)}</Notice>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="destructive" onClick={() => remove.mutate()} disabled={remove.isPending}>
              {remove.isPending ? <LoaderCircle className="animate-spin" /> : <Trash2 data-icon="inline-start" />}
              Delete for everyone
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setOpen(undefined)}>
              Keep it
            </Button>
          </div>
        </div>
      </Collapse>
    </div>
  )
}

function Collapse({ open, children }: { open: boolean; children: ReactNode }) {
  return (
    <AnimatePresence initial={false}>
      {open && (
        <motion.div
          initial={{ opacity: 0, height: 0 }}
          animate={{ opacity: 1, height: 'auto' }}
          exit={{ opacity: 0, height: 0 }}
          transition={{ duration: 0.25, ease: easeOutExpo }}
          className="-mt-1 overflow-hidden"
        >
          {children}
        </motion.div>
      )}
    </AnimatePresence>
  )
}

function possessive(name: string) {
  return name.endsWith('s') ? `${name}’` : `${name}’s`
}
