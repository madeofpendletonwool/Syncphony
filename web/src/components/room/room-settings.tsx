import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Minus, Plus, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import type { ReactNode } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { easeOutExpo } from '@/lib/motion'
import { roomsQuery, type Room } from '@/lib/room'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

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

/**
 * Who can control playback in a room you own, and how turns work. Changes
 * save as you make them, and everyone in the room sees them at once.
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
                permissions: { ...r.permissions, ...body.permissions },
                skipVotePercent: body.skipVotePercent ?? r.skipVotePercent,
                fairnessMode: body.fairnessMode ?? r.fairnessMode,
                fairness: body.fairness ?? r.fairness,
                matching: body.matching ?? r.matching,
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
                    </Dialog.Description>
                  </div>
                  <Dialog.Close asChild>
                    <Button size="icon-sm" variant="ghost" aria-label="Close" className="-mt-1 -mr-1">
                      <X />
                    </Button>
                  </Dialog.Close>
                </div>

                <SectionTitle first hint="You always can, and anyone can skip their own song">Controls</SectionTitle>
                <Setting label="Play and pause">
                  <Levels value={p.playPause} onChange={(playPause) => set({ playPause })} label="Who can play and pause" />
                </Setting>
                <Setting label="Seek" hint="Scrubbing, and restarting the song">
                  <Levels value={p.seek} onChange={(seek) => set({ seek })} label="Who can seek" />
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
                    <ToggleGroupItem value="owner">Only you</ToggleGroupItem>
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
                  <Levels value={p.speaker} onChange={(speaker) => set({ speaker })} label="Who can become the speaker" />
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
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}

function SectionTitle({ children, hint, first }: { children: ReactNode; hint?: string; first?: boolean }) {
  return (
    <div className={cn('-mb-2', !first && 'border-t border-border pt-4')}>
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
        {(users.data ?? []).map((u) => {
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

function Levels({ value, onChange, label }: { value: Level; onChange: (v: Level) => void; label: string }) {
  return (
    <ToggleGroup type="single" value={value} onValueChange={(v) => v && onChange(v as Level)} aria-label={label}>
      <ToggleGroupItem value="everyone">Everyone</ToggleGroupItem>
      <ToggleGroupItem value="owner">Only you</ToggleGroupItem>
    </ToggleGroup>
  )
}
