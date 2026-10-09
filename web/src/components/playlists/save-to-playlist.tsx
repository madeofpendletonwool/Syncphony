import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { BookmarkPlus, Check, LoaderCircle, Plus } from 'lucide-react'
import { useState } from 'react'
import { errorMessage } from '@/api/errors'
import { PlaylistCover } from '@/components/playlists/playlist-cover'
import { Sheet } from '@/components/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useMe } from '@/lib/auth'
import { tap } from '@/lib/haptics'
import { addSongs, createPlaylist, playlistChanged, savedPlaylistsQuery, type SongToSave } from '@/lib/playlists'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/utils'

/**
 * Saves a song to one of your Syncphony playlists, or a new one. The song
 * is from search (`linkId` and `trackId`) or one a room had (`itemId`).
 */
export function SaveToPlaylistButton({ song, title, className }: { song: SongToSave; title: string; className?: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation()
          setOpen(true)
        }}
        aria-label={`Save ${title} to a playlist`}
        title="Save to a playlist"
        className={cn(
          'grid size-9 shrink-0 place-items-center rounded-full text-muted-foreground transition-colors outline-none hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50',
          className,
        )}
      >
        <BookmarkPlus className="size-[1.15rem]" />
      </button>
      <SaveToPlaylistSheet song={song} title={title} open={open} onOpenChange={setOpen} />
    </>
  )
}

export function SaveToPlaylistSheet({
  song,
  title,
  open,
  onOpenChange,
}: {
  song: SongToSave
  title: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const me = useMe()
  const qc = useQueryClient()
  const lists = useQuery({ ...savedPlaylistsQuery, enabled: open })
  const [name, setName] = useState('')
  const [savedTo, setSavedTo] = useState<string>()
  // Yours to change: your own, or any if you're an admin.
  const mine = (lists.data ?? []).filter((p) => p.ownerId === me.id || me.role === 'admin')

  const save = useMutation({
    mutationFn: async (target: { id: string } | { name: string }) => {
      const id = 'id' in target ? target.id : (await createPlaylist(target.name)).id
      return addSongs(id, [song])
    },
    onMutate: () => tap(),
    onSuccess: (p) => {
      playlistChanged(qc, p)
      setSavedTo(p.id)
      setName('')
      toast({ message: `Saved “${title}” to ${p.name}` })
      onOpenChange(false)
    },
    onError: (e) => toast({ message: errorMessage(e), tone: 'error' }),
  })

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="Save to a playlist" description={title}>
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          if (name.trim()) save.mutate({ name: name.trim() })
        }}
      >
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="New playlist" aria-label="New playlist's name" maxLength={100} />
        <Button type="submit" disabled={!name.trim() || save.isPending} aria-label="Make it and save">
          <Plus data-icon="inline-start" />
          New
        </Button>
      </form>
      {lists.isPending ? (
        <LoaderCircle className="mx-auto animate-spin text-muted-foreground" />
      ) : mine.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No playlists yet. Name one above. They live in your <Link to="/library" className="text-primary" onClick={() => onOpenChange(false)}>Library</Link>.
        </p>
      ) : (
        <ul className="-mx-2 flex flex-col">
          {mine.map((p) => (
            <li key={p.id}>
              <button
                type="button"
                disabled={save.isPending}
                onClick={() => save.mutate({ id: p.id })}
                className="flex w-full items-center gap-3 rounded-2xl p-2 text-left transition-colors outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <PlaylistCover playlistId={p.id} songs={p.covers} size={120} className="size-12 rounded-lg shadow-none" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{p.name}</span>
                  <span className="text-sm text-muted-foreground">
                    {p.songCount} song{p.songCount === 1 ? '' : 's'}
                  </span>
                </span>
                {save.isPending && save.variables && 'id' in save.variables && save.variables.id === p.id ? (
                  <LoaderCircle className="size-4 animate-spin" />
                ) : savedTo === p.id ? (
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
