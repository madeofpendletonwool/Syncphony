import { useMutation } from '@tanstack/react-query'
import { X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import { useState, type ReactNode } from 'react'
import { errorMessage } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { easeOutExpo } from '@/lib/motion'
import { BRACKET_SIZES, QUEUE_GAMES, queueGamesOn, startQueueGame, THEMES, type QueueGameKind, type ThemeKind } from '@/lib/queue-games'
import type { Room } from '@/lib/room'
import { toast } from '@/lib/toast'

/**
 * Starts a queue game (MAD-794..796): which one, and how. Connect the
 * artists as the room or as two teams; a theme round to a theme or a
 * surprise; a bracket of 4, 8 or 16 songs, optionally to a theme, played
 * whole or short.
 */
export function QueueGameDialog({ room, open, onOpenChange }: { room: Room; open: boolean; onOpenChange: (open: boolean) => void }) {
  const kinds = queueGamesOn(room.games)
  const [kind, setKind] = useState<QueueGameKind>(kinds[0] ?? 'connect')
  const [teams, setTeams] = useState(1)
  const [theme, setTheme] = useState<ThemeKind | ''>('')
  const [size, setSize] = useState<(typeof BRACKET_SIZES)[number]>(8)
  const [short, setShort] = useState(false)
  const picked = kinds.includes(kind) ? kind : kinds[0]
  const start = useMutation({
    mutationFn: (k: QueueGameKind) =>
      startQueueGame(room.id, {
        kind: k,
        ...(k === 'connect' && { teams }),
        ...(k !== 'connect' && theme && { theme }),
        ...(k === 'bracket' && { size, short }),
      }),
    onSuccess: () => onOpenChange(false),
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

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
                    <Dialog.Title className="text-headline">Queue games</Dialog.Title>
                    <Dialog.Description className="mt-1 text-sm text-muted-foreground">
                      Played through the queue, across songs. The music keeps going.
                    </Dialog.Description>
                  </div>
                  <Dialog.Close asChild>
                    <Button size="icon-sm" variant="ghost" aria-label="Close" className="-mt-1 -mr-1">
                      <X />
                    </Button>
                  </Dialog.Close>
                </div>

                <div className="flex flex-col gap-2">
                  {kinds.map((k) => (
                    <button
                      key={k}
                      type="button"
                      aria-pressed={picked === k}
                      onClick={() => setKind(k)}
                      className={`rounded-2xl border px-3.5 py-3 text-left transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 ${picked === k ? 'border-primary bg-primary/12' : 'border-border hover:bg-muted/60'}`}
                    >
                      <span className="block text-sm font-semibold">{QUEUE_GAMES[k].label}</span>
                      <span className="block text-caption text-muted-foreground">{QUEUE_GAMES[k].hint}</span>
                    </button>
                  ))}
                </div>

                {picked === 'connect' && (
                  <Field label="Who plays">
                    <ToggleGroup type="single" value={String(teams)} onValueChange={(v) => v && setTeams(Number(v))} aria-label="Teams">
                      <ToggleGroupItem value="1">The room together</ToggleGroupItem>
                      <ToggleGroupItem value="2">Two teams race</ToggleGroupItem>
                    </ToggleGroup>
                  </Field>
                )}

                {picked === 'bracket' && (
                  <>
                    <Field label="Songs">
                      <ToggleGroup
                        type="single"
                        value={String(size)}
                        onValueChange={(v) => v && setSize(Number(v) as (typeof BRACKET_SIZES)[number])}
                        aria-label="Bracket size"
                      >
                        {BRACKET_SIZES.map((n) => (
                          <ToggleGroupItem key={n} value={String(n)} className="min-w-11">
                            {n}
                          </ToggleGroupItem>
                        ))}
                      </ToggleGroup>
                    </Field>
                    <label className="flex cursor-pointer items-start justify-between gap-4">
                      <span>
                        <span className="block text-sm font-medium">Short versions</span>
                        <span className="block text-caption text-muted-foreground">About 90 seconds of each song, from its peak</span>
                      </span>
                      <Switch checked={short} onChange={setShort} label="Short versions" />
                    </label>
                  </>
                )}

                {(picked === 'theme' || picked === 'bracket') && (
                  <Field label="Theme">
                    <div className="flex flex-wrap gap-1.5">
                      {[{ id: '' as const, label: picked === 'theme' ? 'Surprise me' : 'None' }, ...THEMES].map((t) => (
                        <button
                          key={t.id}
                          type="button"
                          aria-pressed={theme === t.id}
                          onClick={() => setTheme(t.id)}
                          className={`rounded-full px-3 py-1.5 text-caption font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 ${theme === t.id ? 'bg-primary text-primary-foreground' : 'bg-muted hover:bg-muted/80'}`}
                        >
                          {t.label}
                        </button>
                      ))}
                    </div>
                  </Field>
                )}

                <Button size="lg" disabled={!picked || start.isPending} onClick={() => picked && start.mutate(picked)}>
                  {start.isPending ? 'Starting…' : picked ? `Start ${QUEUE_GAMES[picked].label.toLowerCase()}` : 'Turn queue games on in the room’s settings'}
                </Button>
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <p className="text-sm font-medium">{label}</p>
      {children}
    </div>
  )
}
