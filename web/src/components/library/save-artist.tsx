import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { LoaderCircle, Star } from 'lucide-react'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { Sheet } from '@/components/sheet'
import { Button } from '@/components/ui/button'
import { tap } from '@/lib/haptics'
import {
  artistSaved,
  artistUnsaved,
  findSaved,
  saveArtist,
  savedArtistsQuery,
  unsaveArtist,
  type ArtistToSave,
  type SavedArtist,
} from '@/lib/saved-artists'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'

// Saving an artist (MAD-840): a star, so it doesn't read as the bookmark
// that saves a song to a playlist.

/** Saves or unsaves one artist, with an Undo on the toast. */
function useToggleArtist(artist: ArtistToSave) {
  const qc = useQueryClient()
  const saved = useQuery(savedArtistsQuery)
  const current = findSaved(saved.data, artist.name)

  const fail = (e: unknown) => toast({ message: errorMessage(e), tone: 'error' })
  // Undo can be tapped after this button is gone (now playing closed, say),
  // so it calls the server itself rather than through the mutations.
  const undoSave = (a: SavedArtist) => () =>
    void unsaveArtist(a.id).then(() => artistUnsaved(qc, a.id), fail)
  const undoUnsave = () => void saveArtist(artist).then((a) => artistSaved(qc, a), fail)

  const save = useMutation({
    mutationFn: () => saveArtist(artist),
    onSuccess: (a) => {
      void artistSaved(qc, a)
      toast({ message: `Saved ${a.name} to your Library`, action: { label: 'Undo', onClick: undoSave(a) } })
    },
    onError: fail,
  })
  const unsave = useMutation({
    mutationFn: (a: SavedArtist) => unsaveArtist(a.id),
    onSuccess: (_, a) => {
      void artistUnsaved(qc, a.id)
      toast({ message: `Removed ${a.name} from your Library`, action: { label: 'Undo', onClick: undoUnsave } })
    },
    onError: fail,
  })

  return {
    saved: !!current,
    busy: save.isPending || unsave.isPending,
    toggle: () => {
      tap()
      if (current) unsave.mutate(current)
      else save.mutate()
    },
  }
}

/**
 * A star that saves an artist to your Library. A song with several artists
 * opens a list to pick from. `variant` "button" is the labelled one for
 * artist pages.
 */
export function SaveArtistButton({
  artists,
  variant = 'icon',
  className,
}: {
  artists: ArtistToSave[]
  variant?: 'icon' | 'button'
  className?: string
}) {
  const [picking, setPicking] = useState(false)
  if (artists.length === 0) return null
  if (artists.length === 1) return <ToggleOne artist={artists[0]} variant={variant} className={className} />
  return (
    <>
      <PickerButton artists={artists} onClick={() => setPicking(true)} className={className} />
      <Sheet open={picking} onOpenChange={setPicking} title="Save an artist" description="To your Library, beside your playlists.">
        <ul className="flex flex-col gap-1">
          {artists.map((a) => (
            <PickRow key={`${a.artistIndex ?? ''}-${a.name}`} artist={a} />
          ))}
        </ul>
      </Sheet>
    </>
  )
}

function ToggleOne({ artist, variant, className }: { artist: ArtistToSave; variant: 'icon' | 'button'; className?: string }) {
  const t = useToggleArtist(artist)
  const label = t.saved ? `Remove ${artist.name} from your Library` : `Save ${artist.name} to your Library`
  if (variant === 'button') {
    return (
      <Button size="lg" variant="glass" aria-pressed={t.saved} disabled={t.busy} onClick={t.toggle} className={className}>
        <Star data-icon="inline-start" className={cn(t.saved && 'fill-current text-amber-400')} />
        {t.saved ? 'Saved' : 'Save artist'}
      </Button>
    )
  }
  return <StarButton label={label} saved={t.saved} busy={t.busy} onClick={t.toggle} className={className} />
}

function PickerButton({ artists, onClick, className }: { artists: ArtistToSave[]; onClick: () => void; className?: string }) {
  const saved = useQuery(savedArtistsQuery)
  const any = artists.some((a) => findSaved(saved.data, a.name))
  return <StarButton label="Save one of the artists to your Library" saved={any} onClick={onClick} className={className} />
}

function StarButton({
  label,
  saved,
  busy,
  onClick,
  className,
}: {
  label: string
  saved: boolean
  busy?: boolean
  onClick: () => void
  className?: string
}) {
  return (
    <button
      type="button"
      aria-label={label}
      aria-pressed={saved}
      title={label}
      disabled={busy}
      onClick={(e) => {
        e.stopPropagation()
        onClick()
      }}
      className={cn(
        'grid size-9 shrink-0 place-items-center rounded-full text-muted-foreground transition-colors outline-none hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50',
        saved && 'text-amber-400 hover:text-amber-400',
        className,
      )}
    >
      {busy ? <LoaderCircle className="size-[1.1rem] animate-spin" /> : <Star className={cn('size-[1.1rem]', saved && 'fill-current')} />}
    </button>
  )
}

function PickRow({ artist }: { artist: ArtistToSave }) {
  const t = useToggleArtist(artist)
  return (
    <li className="flex items-center gap-3 rounded-2xl py-1 pr-1 pl-3 hover:bg-muted/50">
      <span className="min-w-0 flex-1 truncate font-medium">{artist.name}</span>
      <StarButton
        label={t.saved ? `Remove ${artist.name} from your Library` : `Save ${artist.name} to your Library`}
        saved={t.saved}
        busy={t.busy}
        onClick={t.toggle}
      />
    </li>
  )
}
