import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { MonitorPlay, Speaker, Trash2, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import { useState, type FormEvent } from 'react'
import { errorMessage } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { BigScreenLook } from './big-screen-look'
import { useMe } from '@/lib/auth'
import { displaysQuery, pairDisplay, setDisplayAudio, unpairDisplay } from '@/lib/displays'
import { easeOutExpo } from '@/lib/motion'
import { can } from '@/lib/playback'
import type { Room } from '@/lib/room'
import { relativeTime } from '@/lib/time'
import { toast } from '@/lib/toast'

/**
 * Pairs a TV, projector or spare tablet with the room: open /tv on it,
 * type in the code it shows, and choose whether it plays the room's audio
 * too. Lists the room's screens, turns their audio on and off, and
 * unpairs them.
 */
export function BigScreenDialog({ room, open, onOpenChange }: { room: Room; open: boolean; onOpenChange: (open: boolean) => void }) {
  const me = useMe()
  const queryClient = useQueryClient()
  const displays = useQuery({ ...displaysQuery(room.id), enabled: open })
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [audio, setAudio] = useState(false)
  // A screen plays as whoever paired it, so it needs their say-so to be the speaker.
  const maySpeak = !me.guest && can(room, me.id, 'speaker')
  const pair = useMutation({
    mutationFn: () => pairDisplay(room.id, code, name.trim() || undefined, maySpeak && audio),
    onSuccess: (d) => {
      setCode('')
      setName('')
      setAudio(false)
      toast({ message: d.audio ? `${d.name} is showing ${room.name}. Press OK on it to play the audio there.` : `${d.name} is showing ${room.name}` })
      void queryClient.invalidateQueries({ queryKey: displaysQuery(room.id).queryKey })
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const toggleAudio = useMutation({
    mutationFn: ({ id, on }: { id: string; on: boolean }) => setDisplayAudio(room.id, id, on),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: displaysQuery(room.id).queryKey }),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const unpair = useMutation({
    mutationFn: (id: string) => unpairDisplay(room.id, id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: displaysQuery(room.id).queryKey }),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (code.trim()) pair.mutate()
  }
  const tvUrl = `${location.origin}/tv`

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
                    <Dialog.Title className="text-headline">Big screen</Dialog.Title>
                    <Dialog.Description className="mt-1 text-sm text-muted-foreground">
                      Put the room on a TV or projector: lyrics, who queued what, and everyone&apos;s reactions. It can play the music too.
                    </Dialog.Description>
                  </div>
                  <Dialog.Close asChild>
                    <Button size="icon-sm" variant="ghost" aria-label="Close" className="-mt-1 -mr-1">
                      <X />
                    </Button>
                  </Dialog.Close>
                </div>

                <ol className="flex flex-col gap-1.5 text-sm text-muted-foreground">
                  <li>
                    1. On the TV, open <span className="font-medium text-foreground select-all">{tvUrl}</span>
                  </li>
                  <li>2. Type in the code it shows.</li>
                </ol>

                <form onSubmit={submit} className="flex flex-col gap-2.5">
                  <Input
                    value={code}
                    onChange={(e) => setCode(e.target.value.toUpperCase())}
                    placeholder="ABC 123"
                    aria-label="Code on the screen"
                    autoComplete="off"
                    autoCapitalize="characters"
                    spellCheck={false}
                    maxLength={16}
                    className="h-14 text-center font-mono text-2xl tracking-[0.3em] md:text-2xl"
                  />
                  <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Name (Living room TV)" aria-label="Screen name" maxLength={40} />
                  {maySpeak && (
                    <label className="flex cursor-pointer items-center justify-between gap-3 rounded-2xl bg-muted/60 px-3.5 py-3">
                      <span className="flex min-w-0 items-start gap-2.5">
                        <Speaker className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                        <span className="min-w-0">
                          <span className="block text-sm font-medium">Play the audio on it too</span>
                          <span className="block text-caption text-muted-foreground">
                            The screen becomes the speaker, through its own speakers or sound system.
                          </span>
                        </span>
                      </span>
                      <Switch checked={audio} onChange={setAudio} label="Play the audio on this screen" />
                    </label>
                  )}
                  <Button type="submit" size="lg" disabled={!code.trim() || pair.isPending}>
                    <MonitorPlay data-icon="inline-start" />
                    {pair.isPending ? 'Pairing…' : 'Pair screen'}
                  </Button>
                </form>

                <Button asChild variant="ghost" size="sm" className="-mt-2 self-center text-muted-foreground">
                  <Link to="/tv">Or show it on this device</Link>
                </Button>

                <BigScreenLook room={room} editable={me.role === 'admin' || room.ownerId === me.id} />

                {(displays.data?.length ?? 0) > 0 && (
                  <section className="flex flex-col gap-2">
                    <h3 className="text-caption font-medium tracking-wide text-muted-foreground uppercase">Paired screens</h3>
                    <ul className="flex flex-col gap-1">
                      {displays.data?.map((d) => {
                        const mayManage = me.role === 'admin' || room.ownerId === me.id || d.pairedBy === me.id
                        return (
                          <li key={d.id} className="flex items-center gap-3 rounded-2xl bg-muted/60 px-3.5 py-2.5">
                            {d.audio ? (
                              <Speaker className="size-4 shrink-0 text-primary" />
                            ) : (
                              <MonitorPlay className="size-4 shrink-0 text-muted-foreground" />
                            )}
                            <div className="min-w-0 flex-1">
                              <p className="truncate text-sm font-medium">{d.name}</p>
                              <p className="text-caption text-muted-foreground">
                                {d.audio ? 'Screen and audio' : 'Screen only'} · Paired {relativeTime(d.createdAt)}
                              </p>
                            </div>
                            {mayManage && (
                              <Switch
                                checked={d.audio}
                                onChange={(on) => !toggleAudio.isPending && toggleAudio.mutate({ id: d.id, on })}
                                label={`Play the audio on ${d.name}`}
                              />
                            )}
                            {mayManage && (
                              <Button
                                size="icon-sm"
                                variant="ghost"
                                aria-label={`Unpair ${d.name}`}
                                disabled={unpair.isPending}
                                onClick={() => unpair.mutate(d.id)}
                              >
                                <Trash2 />
                              </Button>
                            )}
                          </li>
                        )
                      })}
                    </ul>
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
