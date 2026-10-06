import { useMutation, useQueryClient } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import type { ReactNode } from 'react'
import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { easeOutExpo } from '@/lib/motion'
import { roomsQuery, type Room } from '@/lib/room'
import { toast } from '@/lib/toast'

type Change = components['schemas']['UpdateRoomRequest']

// Vote thresholds: a skip passes once more than this percent have voted.
const THRESHOLDS = [
  { percent: 50, label: 'Majority' },
  { percent: 66, label: 'Two-thirds' },
  { percent: 99, label: 'Everyone' },
]

/**
 * Who can control playback in a room you own. Changes save as you make
 * them, and everyone in the room sees them at once.
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
                      Who can control playback. You always can, and anyone can skip their own song.
                    </Dialog.Description>
                  </div>
                  <Dialog.Close asChild>
                    <Button size="icon-sm" variant="ghost" aria-label="Close" className="-mt-1 -mr-1">
                      <X />
                    </Button>
                  </Dialog.Close>
                </div>

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
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
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
