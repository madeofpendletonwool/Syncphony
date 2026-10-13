import { useQuery } from '@tanstack/react-query'
import { LoaderCircle, Plus, Search } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Artwork } from '@/components/artwork'
import { Input } from '@/components/ui/input'
import { artistSearchQuery, fansText, imageUrl } from '@/lib/discover'
import { cn } from '@/lib/utils'

/**
 * Finds any artist to seed a set with, not only ones in your library, and
 * hands the one picked to onPick. Names already in the set show as added.
 */
export function ArtistSearch({
  onPick,
  have,
  autoFocus,
  className,
}: {
  onPick: (name: string) => void
  have: ReadonlySet<string>
  autoFocus?: boolean
  className?: string
}) {
  const [text, setText] = useState('')
  const [q, setQ] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setQ(text.trim()), 250)
    return () => clearTimeout(t)
  }, [text])
  const found = useQuery(artistSearchQuery(q))
  const pick = (name: string) => {
    onPick(name)
    setText('')
    setQ('')
  }

  return (
    <div className={cn('flex flex-col gap-2', className)}>
      <form
        className="relative"
        onSubmit={(e) => {
          e.preventDefault()
          // The top match, or the name as typed.
          const name = found.data?.[0]?.name ?? text.trim()
          if (name) pick(name)
        }}
      >
        <Search className="pointer-events-none absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          autoFocus={autoFocus}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="Add an artist"
          aria-label="Find an artist to add"
          className="pl-10"
          maxLength={200}
        />
        {found.isFetching && <LoaderCircle className="absolute top-1/2 right-3.5 size-4 -translate-y-1/2 animate-spin text-muted-foreground" />}
      </form>
      {q && found.data && found.data.length > 0 && (
        <ul className="-mx-2 flex flex-col">
          {found.data.slice(0, 6).map((a) => {
            const added = have.has(a.name.toLowerCase())
            return (
              <li key={`${a.name}-${a.picture ?? ''}`}>
                <button
                  type="button"
                  disabled={added}
                  onClick={() => pick(a.name)}
                  className="flex w-full items-center gap-3 rounded-2xl p-2 text-left transition-colors outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-60"
                >
                  <Artwork src={imageUrl('artist', a.picture, 120)} className="size-10 rounded-full shadow-none" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{a.name}</span>
                    {a.fans ? <span className="text-caption text-muted-foreground">{fansText(a.fans)}</span> : null}
                  </span>
                  <span className="text-caption text-muted-foreground">{added ? 'Added' : <Plus className="size-4" />}</span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
