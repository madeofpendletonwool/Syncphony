import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Check, Compass, LoaderCircle, Plus } from 'lucide-react'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { Sheet } from '@/components/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { addArtist, createSet, setChanged, setsQuery } from '@/lib/discover'
import { tap } from '@/lib/haptics'
import { toast } from '@/lib/toast'

/** Adds an artist to one of your discovery sets, or a new one (artist pages, ADR 0013). */
export function AddToSetButton({ artist }: { artist: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button size="lg" variant="glass" onClick={() => setOpen(true)}>
        <Compass data-icon="inline-start" />
        Add to discovery set
      </Button>
      <AddToSetSheet artist={artist} open={open} onOpenChange={setOpen} />
    </>
  )
}

export function AddToSetSheet({ artist, open, onOpenChange }: { artist: string; open: boolean; onOpenChange: (open: boolean) => void }) {
  const qc = useQueryClient()
  const sets = useQuery({ ...setsQuery, enabled: open })
  const [name, setName] = useState('')
  const [addedTo, setAddedTo] = useState<string>()
  const add = useMutation({
    mutationFn: async (target: { id: string } | { name: string }) =>
      'id' in target ? addArtist(target.id, artist) : createSet(target.name, [{ name: artist }]),
    onMutate: () => tap(),
    onSuccess: (set) => {
      setChanged(qc, set)
      setAddedTo(set.id)
      setName('')
      toast({ message: `Added ${artist} to ${set.name}` })
      onOpenChange(false)
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="Add to a discovery set" description={`${artist} seeds the set's feed of music you don't have yet.`}>
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          if (name.trim()) add.mutate({ name: name.trim() })
        }}
      >
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="New set" aria-label="New set's name" maxLength={100} />
        <Button type="submit" disabled={!name.trim() || add.isPending}>
          <Plus data-icon="inline-start" />
          New
        </Button>
      </form>
      {sets.isPending ? (
        <LoaderCircle className="mx-auto animate-spin text-muted-foreground" />
      ) : (sets.data ?? []).length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No sets yet. Name one above. They live in{' '}
          <Link to="/discover" className="text-primary" onClick={() => onOpenChange(false)}>
            Discover
          </Link>
          .
        </p>
      ) : (
        <ul className="-mx-2 flex flex-col">
          {(sets.data ?? []).map((s) => (
            <li key={s.id}>
              <button
                type="button"
                disabled={add.isPending}
                onClick={() => add.mutate({ id: s.id })}
                className="flex w-full items-center gap-3 rounded-2xl p-2 text-left transition-colors outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <span className="grid size-12 shrink-0 place-items-center rounded-lg bg-primary/15 text-primary">
                  <Compass className="size-5" />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{s.name}</span>
                  <span className="text-sm text-muted-foreground">
                    {s.artistCount} artist{s.artistCount === 1 ? '' : 's'}
                  </span>
                </span>
                {add.isPending && add.variables && 'id' in add.variables && add.variables.id === s.id ? (
                  <LoaderCircle className="size-4 animate-spin" />
                ) : addedTo === s.id ? (
                  <Check className="size-4 text-primary" />
                ) : null}
              </button>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  )
}
