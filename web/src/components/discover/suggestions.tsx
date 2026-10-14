import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Sparkles, UserX } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { errorMessage } from '@/api/errors'
import { Button } from '@/components/ui/button'
import {
  block,
  blocksQuery,
  dismissSuggestion,
  feedQuery,
  freshQuery,
  importArtists,
  setChanged,
  suggestionsQuery,
  type DiscoveryOffer,
  type DiscoverySet,
} from '@/lib/discover'
import { fadeUp } from '@/lib/motion'
import { toast } from '@/lib/toast'

/**
 * What your thumbs suggest for a set: an artist you keep liking, to add
 * to it, and one you keep turning down, to block.
 */
export function Suggestions({ set }: { set: DiscoverySet }) {
  const qc = useQueryClient()
  const sg = useQuery(suggestionsQuery(set.id))
  const done = () => void qc.invalidateQueries({ queryKey: suggestionsQuery(set.id).queryKey })
  const onError = (e: unknown) => toast({ message: errorMessage(e), tone: 'error' })

  const grow = useMutation({
    mutationFn: (o: DiscoveryOffer) => importArtists(set.id, 'thumb', [o.name]),
    onSuccess: (s, o) => {
      setChanged(qc, s)
      done()
      toast({ message: `Added ${o.name} to ${s.name}` })
    },
    onError,
  })
  const blockIt = useMutation({
    mutationFn: (o: DiscoveryOffer) => block(o.name),
    onSuccess: (b) => {
      done()
      void qc.invalidateQueries({ queryKey: blocksQuery.queryKey })
      void qc.invalidateQueries({ queryKey: feedQuery(set.id).queryKey })
      void qc.invalidateQueries({ queryKey: freshQuery(set.id).queryKey })
      toast({ message: `No more ${b.name}` })
    },
    onError,
  })
  const no = useMutation({
    mutationFn: ({ kind, o }: { kind: 'grow' | 'block'; o: DiscoveryOffer }) => dismissSuggestion(set.id, kind, o.name),
    onSuccess: done,
    onError,
  })

  const offers = [
    ...(sg.data?.grow ?? []).map((o) => ({ kind: 'grow' as const, o })),
    ...(sg.data?.block ?? []).map((o) => ({ kind: 'block' as const, o })),
  ].slice(0, 3)
  const busy = grow.isPending || blockIt.isPending || no.isPending

  return (
    <AnimatePresence initial={false}>
      {offers.length > 0 && (
        <motion.ul variants={fadeUp} initial="hidden" animate="show" exit={{ opacity: 0, height: 0 }} className="mt-3 flex flex-col gap-2" aria-label="Suggestions">
          {offers.map(({ kind, o }) => (
            <li key={`${kind}-${o.key}`} className="glass flex flex-col gap-2 rounded-3xl p-3 sm:flex-row sm:items-center">
              <span className={kind === 'grow' ? 'text-primary' : 'text-muted-foreground'}>
                {kind === 'grow' ? <Sparkles className="size-5" /> : <UserX className="size-5" />}
              </span>
              <p className="min-w-0 flex-1 text-sm">
                {kind === 'grow' ? (
                  <>
                    You liked {o.thumbs} songs by <span className="font-medium">{o.name}</span>. Add them to this set?
                  </>
                ) : (
                  <>
                    You turned down {o.thumbs} songs by <span className="font-medium">{o.name}</span>. Not for you?
                  </>
                )}
              </p>
              <div className="flex justify-end gap-1">
                <Button variant="ghost" size="sm" disabled={busy} onClick={() => no.mutate({ kind, o })}>
                  No thanks
                </Button>
                <Button size="sm" disabled={busy} onClick={() => (kind === 'grow' ? grow.mutate(o) : blockIt.mutate(o))}>
                  {kind === 'grow' ? 'Add' : 'Block'}
                </Button>
              </div>
            </li>
          ))}
        </motion.ul>
      )}
    </AnimatePresence>
  )
}
