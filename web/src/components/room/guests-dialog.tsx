import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, QrCode as QrIcon, RefreshCw, Share2, TicketX, UserMinus, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { QrCode } from '@/components/tv/qr-code'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { UserAvatar } from '@/components/user-avatar'
import { useMe } from '@/lib/auth'
import { createGuestPass, guestPassQuery, guestsQuery, kickGuest, PASS_LENGTHS, revokeGuestPass } from '@/lib/guests'
import { easeOutExpo } from '@/lib/motion'
import type { Room } from '@/lib/room'
import { relativeTime } from '@/lib/time'
import { toast } from '@/lib/toast'

type Length = (typeof PASS_LENGTHS)[number]['id']

/**
 * The room's guest pass as a QR code, for friends of friends to scan and
 * add songs without an account; and who's in as a guest.
 */
export function GuestsDialog({ room, open, onOpenChange }: { room: Room; open: boolean; onOpenChange: (open: boolean) => void }) {
  const me = useMe()
  const queryClient = useQueryClient()
  const host = me.role === 'admin' || room.ownerId === me.id
  const pass = useQuery({ ...guestPassQuery(room.id), enabled: open && room.guests.allowed })
  const guests = useQuery({ ...guestsQuery(room.id), enabled: open })
  const [length, setLength] = useState<Length>('night')
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: guestPassQuery(room.id).queryKey })
    void queryClient.invalidateQueries({ queryKey: guestsQuery(room.id).queryKey })
  }
  const start = useMutation({
    mutationFn: () => createGuestPass(room.id, PASS_LENGTHS.find((l) => l.id === length)!.until(new Date())),
    onSuccess: (p) => queryClient.setQueryData(guestPassQuery(room.id).queryKey, p),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const revoke = useMutation({
    mutationFn: () => revokeGuestPass(room.id),
    onSuccess: () => {
      queryClient.setQueryData(guestPassQuery(room.id).queryKey, null)
      toast({ message: 'No one else can join with that code' })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const kick = useMutation({
    mutationFn: (userId: string) => kickGuest(room.id, userId),
    onSuccess: refresh,
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const p = pass.data
  const mayRevoke = host || p?.createdBy === me.id

  const share = async (url: string) => {
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
                className="glass-strong fixed inset-x-0 bottom-0 z-50 mx-auto flex max-h-[90dvh] w-full max-w-md flex-col gap-5 overflow-y-auto rounded-t-3xl p-5 pb-[calc(env(safe-area-inset-bottom)+1.25rem)] shadow-float outline-none sm:inset-x-4 sm:top-1/2 sm:bottom-auto sm:-translate-y-1/2 sm:rounded-3xl sm:pb-5"
              >
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <Dialog.Title className="text-headline">Invite guests</Dialog.Title>
                    <Dialog.Description className="mt-1 text-sm text-muted-foreground">
                      {room.guests.allowed
                        ? `They scan the code, pick a name, and add songs. No account needed${room.guests.maxSongs > 0 ? `, up to ${room.guests.maxSongs} songs each` : ''}.`
                        : 'Let friends of friends add songs without an account.'}
                    </Dialog.Description>
                  </div>
                  <Dialog.Close asChild>
                    <Button size="icon-sm" variant="ghost" aria-label="Close" className="-mt-1 -mr-1">
                      <X />
                    </Button>
                  </Dialog.Close>
                </div>

                {!room.guests.allowed ? (
                  <p className="rounded-2xl bg-muted/60 px-4 py-3 text-sm text-muted-foreground">
                    {room.ownerId === me.id
                      ? 'Turn on guests in Room settings first.'
                      : "This room doesn't let guests join. Its owner can turn that on in Room settings."}
                  </p>
                ) : p ? (
                  <motion.section
                    key={p.id}
                    initial={{ opacity: 0, scale: 0.96 }}
                    animate={{ opacity: 1, scale: 1 }}
                    transition={{ duration: 0.4, ease: easeOutExpo }}
                    className="flex flex-col items-center gap-3"
                  >
                    <div className="rounded-3xl bg-white p-4 text-black shadow-float">
                      <QrCode value={p.url} label={`Scan to join ${room.name} as a guest`} className="size-56" />
                    </div>
                    <p className="text-sm text-muted-foreground">Works until {new Date(p.expiresAt).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })} ({relativeTime(p.expiresAt)})</p>
                    <div className="flex flex-wrap justify-center gap-2">
                      <Button variant="secondary" size="sm" onClick={() => void share(p.url)}>
                        {typeof navigator.share === 'function' ? <Share2 data-icon="inline-start" /> : <Copy data-icon="inline-start" />}
                        {typeof navigator.share === 'function' ? 'Share link' : 'Copy link'}
                      </Button>
                      <Button variant="ghost" size="sm" disabled={start.isPending} onClick={() => start.mutate()}>
                        <RefreshCw data-icon="inline-start" />
                        New code
                      </Button>
                      {mayRevoke && (
                        <Button variant="ghost" size="sm" disabled={revoke.isPending} onClick={() => revoke.mutate()} className="text-destructive">
                          <TicketX data-icon="inline-start" />
                          Stop
                        </Button>
                      )}
                    </div>
                  </motion.section>
                ) : (
                  <section className="flex flex-col gap-3">
                    <div>
                      <p className="text-sm font-medium">How long</p>
                      <p className="text-caption text-muted-foreground">Guests sign out on their own when the code runs out</p>
                    </div>
                    <ToggleGroup type="single" value={length} onValueChange={(v) => v && setLength(v as Length)} aria-label="How long the code works">
                      {PASS_LENGTHS.map((l) => (
                        <ToggleGroupItem key={l.id} value={l.id}>
                          {l.label}
                        </ToggleGroupItem>
                      ))}
                    </ToggleGroup>
                    <Button size="lg" disabled={start.isPending || pass.isPending} onClick={() => start.mutate()}>
                      <QrIcon data-icon="inline-start" />
                      Show guest code
                    </Button>
                  </section>
                )}

                {(guests.data?.length ?? 0) > 0 && (
                  <section className="flex flex-col gap-2">
                    <h3 className="text-caption font-medium tracking-wide text-muted-foreground uppercase">Guests here</h3>
                    <ul className="flex flex-col gap-1">
                      {guests.data?.map((g) => (
                        <li key={g.user.id} className="flex items-center gap-3 rounded-2xl bg-muted/60 px-3.5 py-2.5">
                          <UserAvatar user={g.user} className="size-8 text-xs" />
                          <div className="min-w-0 flex-1">
                            <p className="truncate text-sm font-medium">{g.user.displayName}</p>
                            <p className="text-caption text-muted-foreground">
                              {g.songs} {room.guests.maxSongs > 0 ? `of ${room.guests.maxSongs} ` : ''}song{g.songs === 1 && room.guests.maxSongs === 0 ? '' : 's'} · joined {relativeTime(g.joinedAt)}
                            </p>
                          </div>
                          {host && (
                            <Button
                              size="icon-sm"
                              variant="ghost"
                              aria-label={`Remove ${g.user.displayName}`}
                              disabled={kick.isPending}
                              onClick={() => kick.mutate(g.user.id)}
                            >
                              <UserMinus />
                            </Button>
                          )}
                        </li>
                      ))}
                    </ul>
                    {host && <p className="text-caption text-muted-foreground">Removing a guest signs them out and takes their waiting songs out of the queue.</p>}
                  </section>
                )}
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}
