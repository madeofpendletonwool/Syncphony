import { useNavigate, useRouterState } from '@tanstack/react-router'
import { Keyboard, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import { Fragment, useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { easeOutExpo } from '@/lib/motion'
import { usePlayer } from '@/lib/now-playing'
import { SEARCH_KEYS, SHORTCUTS, shortcutOf } from '@/lib/shortcuts'

/** Gives the search page's box an id the `/` shortcut can find. */
export const SEARCH_INPUT_ID = 'search-input'

/**
 * Keyboard shortcuts for a computer (MAD-735): space or K plays and pauses, N
 * skips (or votes to), / or ⌘K searches, L shows lyrics, ? lists them all.
 * Never while typing, and not under another dialog; now playing is the one
 * that lets them through. Play and skip do only what the room lets you.
 */
export function KeyboardShortcuts({
  expanded,
  onExpandedChange,
  onLyricsChange,
}: {
  expanded: boolean
  onExpandedChange: (open: boolean) => void
  onLyricsChange: (toggle: (lyrics: boolean) => boolean) => void
}) {
  const { nowPlaying: np, commands } = usePlayer()
  const navigate = useNavigate()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const [help, setHelp] = useState(false)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const shortcut = shortcutOf(e)
      if (!shortcut) return
      if (help || document.querySelector('[role="dialog"]:not([data-shortcuts])')) {
        if (shortcut === 'help' && help) {
          e.preventDefault()
          setHelp(false)
        }
        return
      }
      switch (shortcut) {
        case 'playPause':
          // Space would scroll the page whether or not you may pause.
          e.preventDefault()
          commands.toggle?.()
          return
        case 'skip':
          if (commands.next) commands.next()
          else commands.vote?.toggle()
          return
        case 'search':
          e.preventDefault()
          onExpandedChange(false)
          if (pathname === '/search') {
            const input = document.getElementById(SEARCH_INPUT_ID)
            if (input instanceof HTMLInputElement) {
              input.focus()
              input.select()
            }
          } else void navigate({ to: '/search' })
          return
        case 'lyrics':
          // Lyrics live in now playing, in place of the artwork.
          if (!np?.roomId) return
          if (expanded) onLyricsChange((l) => !l)
          else {
            onLyricsChange(() => true)
            onExpandedChange(true)
          }
          return
        case 'help':
          e.preventDefault()
          setHelp(true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [help, commands, np?.roomId, expanded, pathname, navigate, onExpandedChange, onLyricsChange])

  return <ShortcutsDialog open={help} onOpenChange={setHelp} />
}

function ShortcutsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <Dialog.Portal forceMount>
            <Dialog.Overlay asChild forceMount>
              <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} className="fixed inset-0 z-[60] bg-black/40" />
            </Dialog.Overlay>
            <Dialog.Content asChild forceMount>
              <motion.div
                initial={{ opacity: 0, y: 24 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: 24 }}
                transition={{ duration: 0.35, ease: easeOutExpo }}
                className="glass-strong fixed inset-x-4 top-1/2 z-[60] mx-auto flex w-auto max-w-sm -translate-y-1/2 flex-col gap-5 rounded-3xl p-5 shadow-float outline-none"
              >
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <Dialog.Title className="flex items-center gap-2 text-headline">
                      <Keyboard className="size-5 text-muted-foreground" />
                      Keyboard shortcuts
                    </Dialog.Title>
                    <Dialog.Description className="mt-1 text-sm text-muted-foreground">
                      They wait while you&apos;re typing.
                    </Dialog.Description>
                  </div>
                  <Dialog.Close asChild>
                    <Button size="icon-sm" variant="ghost" aria-label="Close" className="-mt-1 -mr-1">
                      <X />
                    </Button>
                  </Dialog.Close>
                </div>
                <Keys rows={SHORTCUTS} />
                <section className="flex flex-col gap-2">
                  <h3 className="text-caption font-medium text-muted-foreground">In search</h3>
                  <Keys rows={SEARCH_KEYS} />
                </section>
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}

// ⌘ on a Mac, Ctrl everywhere else.
const MOD = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.userAgent) ? '⌘' : 'Ctrl'

function Keys({ rows }: { rows: { keys: string[][]; label: string }[] }) {
  return (
    <dl className="flex flex-col gap-2.5">
      {rows.map((r) => (
        <div key={r.label} className="flex items-center justify-between gap-4">
          <dt className="text-sm">{r.label}</dt>
          <dd className="flex shrink-0 items-center gap-1.5 text-caption text-muted-foreground">
            {r.keys.map((combo, i) => (
              <Fragment key={combo.join('+')}>
                {i > 0 && <span>or</span>}
                <span className="flex gap-1">
                  {combo.map((k) => (
                    <kbd
                      key={k}
                      className="grid h-6 min-w-6 place-items-center rounded-md bg-foreground/10 px-1.5 font-sans text-xs font-medium text-foreground"
                    >
                      {k === '⌘' ? MOD : k}
                    </kbd>
                  ))}
                </span>
              </Fragment>
            ))}
          </dd>
        </div>
      ))}
    </dl>
  )
}
