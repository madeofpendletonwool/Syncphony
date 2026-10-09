import { X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { easeOutExpo } from '@/lib/motion'

/**
 * A dialog that rises from the bottom on phones and sits centered on a
 * computer, with a title and a close button.
 */
export function Sheet({
  open,
  onOpenChange,
  title,
  description,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: ReactNode
  children: ReactNode
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <AnimatePresence>
        {open && (
          <Dialog.Portal forceMount>
            <Dialog.Overlay asChild forceMount>
              <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} className="fixed inset-0 z-[70] bg-black/40" />
            </Dialog.Overlay>
            <Dialog.Content asChild forceMount>
              <motion.div
                initial={{ opacity: 0, y: 24 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: 24 }}
                transition={{ duration: 0.35, ease: easeOutExpo }}
                onClick={(e) => e.stopPropagation()}
                className="glass-strong fixed inset-x-0 bottom-0 z-[70] mx-auto flex max-h-[90dvh] w-full max-w-md flex-col gap-5 overflow-y-auto rounded-t-3xl p-5 pb-[calc(env(safe-area-inset-bottom)+1.25rem)] shadow-float outline-none sm:inset-x-4 sm:top-1/2 sm:bottom-auto sm:-translate-y-1/2 sm:rounded-3xl sm:pb-5"
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <Dialog.Title className="text-headline">{title}</Dialog.Title>
                    {description ? (
                      <Dialog.Description className="mt-1 text-sm text-muted-foreground">{description}</Dialog.Description>
                    ) : (
                      <Dialog.Description className="sr-only">{title}</Dialog.Description>
                    )}
                  </div>
                  <Dialog.Close asChild>
                    <Button size="icon-sm" variant="ghost" aria-label="Close" className="-mt-1 -mr-1">
                      <X />
                    </Button>
                  </Dialog.Close>
                </div>
                {children}
              </motion.div>
            </Dialog.Content>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}
