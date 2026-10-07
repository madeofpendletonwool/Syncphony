import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Crown, Link2, LoaderCircle, LogOut, RefreshCw, Share2, Trash2, UserMinus, UserPlus, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import { useState, type ReactNode } from 'react'
import { errorMessage } from '@/api/errors'
import { Notice } from '@/components/notice'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import {
  addMember,
  createInvite,
  describeInvite,
  INVITE_LENGTHS,
  invitesQuery,
  mayInvite,
  membersQuery,
  removeMember,
  revokeInvite,
  visibility,
  type RoomInvite,
} from '@/lib/access'
import { useMe } from '@/lib/auth'
import { easeOutExpo } from '@/lib/motion'
import { leaveRoom, roomsQuery, type Room } from '@/lib/room'
import { toast } from '@/lib/toast'
import { usersQuery } from '@/lib/users'

type Length = (typeof INVITE_LENGTHS)[number]['id']

/**
 * Who's in a room that isn't open, and how to get more people in: the
 * room's link (unlisted), invites and requests to join (private), and
 * adding people directly. Also where a member leaves for good.
 */
export function MembersDialog({ room, open, onOpenChange }: { room: Room; open: boolean; onOpenChange: (open: boolean) => void }) {
  const me = useMe()
  const queryClient = useQueryClient()
  const host = me.role === 'admin' || room.ownerId === me.id
  const closed = room.visibility !== 'open'
  const members = useQuery({ ...membersQuery(room.id), enabled: open && closed })
  const users = useQuery(usersQuery)
  const [adding, setAdding] = useState(false)
  const [leaving, setLeaving] = useState(false)
  const refresh = () => void queryClient.invalidateQueries({ queryKey: membersQuery(room.id).queryKey })

  const add = useMutation({
    mutationFn: (userId: string) => addMember(room.id, userId),
    onSuccess: refresh,
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const remove = useMutation({
    mutationFn: (userId: string) => removeMember(room.id, userId),
    onSuccess: refresh,
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const leave = useMutation({
    mutationFn: () => removeMember(room.id, me.id),
    onSuccess: () => {
      onOpenChange(false)
      leaveRoom()
      queryClient.setQueryData(roomsQuery.queryKey, (rs) => rs?.filter((r) => r.id !== room.id))
      toast({ message: `You left ${room.name}` })
    },
  })

  const all = members.data ?? []
  const pending = all.filter((m) => m.status === 'pending')
  const joined = all.filter((m) => m.status === 'member' && m.user.id !== room.ownerId)
  const owner = users.data?.find((u) => u.id === room.ownerId)
  const inRoom = new Set([room.ownerId, ...all.map((m) => m.user.id)])
  const addable = (users.data ?? []).filter((u) => !u.guest && !u.removed && !u.disabled && !inRoom.has(u.id))
  const v = visibility(room.visibility)

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <Dialog.Portal forceMount>
            <Dialog.Overlay asChild forceMount>
              <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} className="fixed inset-0 z-50 bg-black/40" />
            </Dialog.Overlay>
            <Dialog.Content asChild forceMount>
              <motion.div
                initial={{ opacity: 0, y: 24 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: 24 }}
                transition={{ duration: 0.35, ease: easeOutExpo }}
                className="glass-strong fixed inset-x-0 bottom-0 z-50 mx-auto flex max-h-[90dvh] w-full max-w-md flex-col gap-5 overflow-y-auto *:shrink-0 rounded-t-3xl p-5 pb-[calc(env(safe-area-inset-bottom)+1.25rem)] shadow-float outline-none sm:inset-x-4 sm:top-1/2 sm:bottom-auto sm:-translate-y-1/2 sm:rounded-3xl sm:pb-5"
              >
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <Dialog.Title className="flex items-center gap-2 text-headline">
                      <v.icon className="size-5 text-muted-foreground" />
                      Members
                    </Dialog.Title>
                    <Dialog.Description className="mt-1 text-sm text-muted-foreground">
                      {closed ? `${v.label}: ${v.hint.toLowerCase()}.` : 'Everyone on this server can join this room.'}
                    </Dialog.Description>
                  </div>
                  <Dialog.Close asChild>
                    <Button size="icon-sm" variant="ghost" aria-label="Close" className="-mt-1 -mr-1">
                      <X />
                    </Button>
                  </Dialog.Close>
                </div>

                {!closed ? (
                  <p className="rounded-2xl bg-muted/60 px-4 py-3 text-sm text-muted-foreground">
                    {host
                      ? 'To choose who’s in, make it unlisted or private in Room settings.'
                      : 'Its owner can make it unlisted or private in Room settings.'}
                  </p>
                ) : (
                  <>
                    {host && pending.length > 0 && (
                      <Section title="Asking to join">
                        <ul className="flex flex-col gap-1">
                          {pending.map((m) => (
                            <li key={m.user.id} className="flex items-center gap-3 rounded-2xl bg-primary/10 px-3.5 py-2.5">
                              <UserAvatar user={m.user} className="size-8 text-xs" />
                              <p className="min-w-0 flex-1 truncate text-sm font-medium">{m.user.displayName}</p>
                              <Button size="sm" disabled={add.isPending} onClick={() => add.mutate(m.user.id)}>
                                <Check data-icon="inline-start" />
                                Let in
                              </Button>
                              <Button
                                size="icon-sm"
                                variant="ghost"
                                aria-label={`Turn down ${m.user.displayName}`}
                                disabled={remove.isPending}
                                onClick={() => remove.mutate(m.user.id)}
                              >
                                <X />
                              </Button>
                            </li>
                          ))}
                        </ul>
                      </Section>
                    )}

                    {mayInvite(room, me) &&
                      (room.visibility === 'unlisted' ? <RoomLink room={room} /> : <PrivateInvites room={room} />)}

                    <Section
                      title="In this room"
                      action={
                        host && (
                          <Button size="xs" variant="ghost" aria-expanded={adding} onClick={() => setAdding((a) => !a)} disabled={addable.length === 0}>
                            <UserPlus data-icon="inline-start" />
                            Add people
                          </Button>
                        )
                      }
                    >
                      <Collapse open={adding}>
                        <ul className="mb-2 flex max-h-56 flex-col gap-0.5 overflow-y-auto rounded-2xl bg-muted/60 p-2">
                          {addable.map((u) => (
                            <li key={u.id}>
                              <button
                                type="button"
                                disabled={add.isPending}
                                onClick={() => add.mutate(u.id)}
                                className="flex w-full items-center gap-3 rounded-xl px-2 py-1.5 text-left text-sm transition-colors outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
                              >
                                <UserAvatar user={u} className="size-7 text-[0.65rem]" />
                                <span className="min-w-0 flex-1 truncate">{u.displayName}</span>
                                {add.isPending && add.variables === u.id ? (
                                  <LoaderCircle className="size-4 animate-spin" />
                                ) : (
                                  <UserPlus className="size-4 text-muted-foreground" />
                                )}
                              </button>
                            </li>
                          ))}
                        </ul>
                      </Collapse>
                      <ul className="flex flex-col gap-1">
                        {owner && (
                          <li className="flex items-center gap-3 rounded-2xl px-1 py-1">
                            <UserAvatar user={owner} className="size-8 text-xs" />
                            <p className="min-w-0 flex-1 truncate text-sm font-medium">{owner.id === me.id ? 'You' : owner.displayName}</p>
                            <span className="flex items-center gap-1 text-caption text-muted-foreground">
                              <Crown className="size-3.5" />
                              Owner
                            </span>
                          </li>
                        )}
                        {joined.map((m) => (
                          <li key={m.user.id} className="flex items-center gap-3 rounded-2xl px-1 py-1">
                            <UserAvatar user={m.user} className="size-8 text-xs" />
                            <p className="min-w-0 flex-1 truncate text-sm">{m.user.id === me.id ? 'You' : m.user.displayName}</p>
                            {host && m.user.id !== me.id && (
                              <Button
                                size="icon-sm"
                                variant="ghost"
                                aria-label={`Remove ${m.user.displayName}`}
                                disabled={remove.isPending}
                                onClick={() => remove.mutate(m.user.id)}
                              >
                                <UserMinus />
                              </Button>
                            )}
                          </li>
                        ))}
                        {members.isPending && <li className="px-1 py-2 text-sm text-muted-foreground">Loading…</li>}
                      </ul>
                      {host && joined.length > 0 && (
                        <p className="mt-1 text-caption text-muted-foreground">
                          Removing someone takes their waiting songs out of the queue. They can come back only if they&apos;re let in again.
                        </p>
                      )}
                    </Section>

                    {room.ownerId !== me.id && joined.some((m) => m.user.id === me.id) && (
                      <div className="flex flex-col gap-3 border-t border-border pt-4">
                        {!leaving ? (
                          <Button variant="ghost" size="sm" className="self-start hover:text-destructive" onClick={() => setLeaving(true)}>
                            <LogOut data-icon="inline-start" />
                            Leave for good
                          </Button>
                        ) : (
                          <div className="flex flex-col gap-3 rounded-2xl bg-destructive/10 p-3">
                            <p className="text-sm">
                              Leave <span className="font-medium">{room.name}</span>? Your waiting songs come out of the queue, and you&apos;ll need
                              {room.visibility === 'private' ? ' to be let in' : ' the link'} to come back.
                            </p>
                            <Notice>{leave.error && errorMessage(leave.error)}</Notice>
                            <div className="flex flex-wrap gap-2">
                              <Button size="sm" variant="destructive" disabled={leave.isPending} onClick={() => leave.mutate()}>
                                {leave.isPending ? <LoaderCircle className="animate-spin" /> : <LogOut data-icon="inline-start" />}
                                Leave
                              </Button>
                              <Button size="sm" variant="ghost" onClick={() => setLeaving(false)}>
                                Stay
                              </Button>
                            </div>
                          </div>
                        )}
                      </div>
                    )}
                  </>
                )}
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}

/** An unlisted room's link: one to share, which anyone in the room can renew. */
function RoomLink({ room }: { room: Room }) {
  const me = useMe()
  const queryClient = useQueryClient()
  const invites = useQuery(invitesQuery(room.id))
  const link = invites.data?.find((i) => !i.expiresAt && !i.maxUses) ?? invites.data?.[0]
  const set = () => void queryClient.invalidateQueries({ queryKey: invitesQuery(room.id).queryKey })
  const make = useMutation({
    mutationFn: async () => {
      const fresh = await createInvite(room.id, {})
      // Renewing retires the old link, where you may.
      if (link && (link.createdBy === me.id || room.ownerId === me.id || me.role === 'admin')) await revokeInvite(room.id, link.code)
      return fresh
    },
    onSuccess: set,
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  return (
    <Section title="Room link" hint="Anyone on this server with the link can join. Anyone in the room can share it">
      {invites.isPending ? (
        <div className="h-11 animate-pulse rounded-2xl bg-muted/60" />
      ) : link ? (
        <InviteRow room={room} invite={link} renew={() => make.mutate()} renewing={make.isPending} />
      ) : (
        <Button className="self-start" disabled={make.isPending} onClick={() => make.mutate()}>
          {make.isPending ? <LoaderCircle className="animate-spin" /> : <Link2 data-icon="inline-start" />}
          Make a link
        </Button>
      )}
    </Section>
  )
}

/** A private room's invites: for the owner, each lasting a while or for one person. */
function PrivateInvites({ room }: { room: Room }) {
  const queryClient = useQueryClient()
  const invites = useQuery(invitesQuery(room.id))
  const [length, setLength] = useState<Length>('week')
  const [once, setOnce] = useState(false)
  const set = () => void queryClient.invalidateQueries({ queryKey: invitesQuery(room.id).queryKey })
  const make = useMutation({
    mutationFn: () => {
      const until = INVITE_LENGTHS.find((l) => l.id === length)!.until(new Date())
      return createInvite(room.id, { expiresAt: until?.toISOString(), maxUses: once ? 1 : undefined })
    },
    onSuccess: (inv) => {
      set()
      void share(room, inv.url)
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  return (
    <Section
      title="Invite links"
      hint={room.approveJoins ? 'People who use one ask to join, and you let them in' : 'Anyone on this server with one joins straight away'}
    >
      {(invites.data?.length ?? 0) > 0 && (
        <ul className="flex flex-col gap-1">
          {invites.data?.map((inv) => (
            <li key={inv.code}>
              <InviteRow room={room} invite={inv} />
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-col gap-3 rounded-2xl bg-muted/60 p-3">
        <ToggleGroup type="single" value={length} onValueChange={(v) => v && setLength(v as Length)} aria-label="How long the link works" className="flex-wrap">
          {INVITE_LENGTHS.map((l) => (
            <ToggleGroupItem key={l.id} value={l.id}>
              {l.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <label className="flex cursor-pointer items-center justify-between gap-4">
          <span className="text-sm">One person only</span>
          <Switch checked={once} onChange={setOnce} label="One person only" />
        </label>
        <Button className="self-start" disabled={make.isPending} onClick={() => make.mutate()}>
          {make.isPending ? <LoaderCircle className="animate-spin" /> : <Link2 data-icon="inline-start" />}
          New invite link
        </Button>
      </div>
    </Section>
  )
}

function InviteRow({ room, invite, renew, renewing }: { room: Room; invite: RoomInvite; renew?: () => void; renewing?: boolean }) {
  const me = useMe()
  const queryClient = useQueryClient()
  const revoke = useMutation({
    mutationFn: () => revokeInvite(room.id, invite.code),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: invitesQuery(room.id).queryKey })
      toast({ message: 'That link no longer works' })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const mayRevoke = invite.createdBy === me.id || room.ownerId === me.id || me.role === 'admin'
  const canShare = typeof navigator.share === 'function'
  return (
    <div className="flex items-center gap-2 rounded-2xl bg-muted/60 py-1.5 pr-1.5 pl-3.5">
      <div className="min-w-0 flex-1">
        <p className="truncate font-mono text-xs">{invite.url.replace(/^https?:\/\//, '')}</p>
        <p className="text-caption text-muted-foreground">{describeInvite(invite)}</p>
      </div>
      <Button size="icon-sm" variant="ghost" aria-label={canShare ? 'Share link' : 'Copy link'} onClick={() => void share(room, invite.url)}>
        {canShare ? <Share2 /> : <Copy />}
      </Button>
      {renew && (
        <Button size="icon-sm" variant="ghost" aria-label="New link" disabled={renewing} onClick={renew}>
          <RefreshCw />
        </Button>
      )}
      {mayRevoke && (
        <Button size="icon-sm" variant="ghost" aria-label="Revoke link" disabled={revoke.isPending} onClick={() => revoke.mutate()} className="hover:text-destructive">
          <Trash2 />
        </Button>
      )}
    </div>
  )
}

async function share(room: Room, url: string) {
  try {
    if (navigator.share) await navigator.share({ title: `Join ${room.name} on Syncphony`, url })
    else {
      await navigator.clipboard.writeText(url)
      toast({ message: 'Link copied' })
    }
  } catch {
    // Share sheet dismissed.
  }
}

function Section({ title, hint, action, children }: { title: string; hint?: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h3 className="text-caption font-medium tracking-wide text-muted-foreground uppercase">{title}</h3>
          {hint && <p className="text-caption text-muted-foreground">{hint}</p>}
        </div>
        {action}
      </div>
      {children}
    </section>
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
          className="overflow-hidden"
        >
          {children}
        </motion.div>
      )}
    </AnimatePresence>
  )
}
