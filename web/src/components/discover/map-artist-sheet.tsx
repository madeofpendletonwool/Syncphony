import { useQuery } from '@tanstack/react-query'
import { Crosshair, LoaderCircle, Plus, UserX } from 'lucide-react'
import { errorMessage } from '@/api/errors'
import { Artwork } from '@/components/artwork'
import { Notice } from '@/components/notice'
import { Sheet } from '@/components/sheet'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { artistSongsQuery, fansText, imageUrl, type DiscoveryMapNode } from '@/lib/discover'
import { SongRow } from './song-row'

/**
 * An artist on the map: their best-known songs you don't have, each with
 * a preview and a YouTube link, and what to do next: re-center the map on
 * them, add them to a set, or say they aren't for you.
 */
export function MapArtistSheet({
  node,
  onOpenChange,
  onRecenter,
  onAddToSet,
  addTo,
  onBlock,
}: {
  node: DiscoveryMapNode | undefined
  onOpenChange: (open: boolean) => void
  onRecenter: (n: DiscoveryMapNode) => void
  /** Opens the list of sets to add them to. */
  onAddToSet: (n: DiscoveryMapNode) => void
  /** The set the map is of, to add them to it in one tap. */
  addTo?: { name: string; add: (n: DiscoveryMapNode) => void; pending: boolean }
  onBlock: (n: DiscoveryMapNode) => void
}) {
  const facts = node
    ? [node.owned ? 'In your library' : 'New to you', fansText(node.fans), ...node.tags.slice(0, 2)].filter(Boolean).join(' · ')
    : ''
  return (
    <Sheet open={!!node} onOpenChange={onOpenChange} title={node?.name ?? ''} description={facts}>
      {node && (
        <>
          <div className="flex items-center gap-3">
            <Artwork src={imageUrl('artist', node.picture, 250)} className="size-16 rounded-full shadow-none" />
            <div className="flex min-w-0 flex-1 flex-wrap gap-2">
              {!node.center && (
                <Button size="sm" onClick={() => onRecenter(node)}>
                  <Crosshair data-icon="inline-start" />
                  Re-center here
                </Button>
              )}
              {addTo && !node.seed ? (
                <Button size="sm" variant="glass" disabled={addTo.pending} onClick={() => addTo.add(node)}>
                  {addTo.pending ? <LoaderCircle data-icon="inline-start" className="animate-spin" /> : <Plus data-icon="inline-start" />}
                  Add to {addTo.name}
                </Button>
              ) : null}
              <Button size="sm" variant="glass" onClick={() => onAddToSet(node)}>
                <Plus data-icon="inline-start" />
                {addTo ? 'Other set' : 'Add to set'}
              </Button>
              {!node.seed && (
                <Button size="sm" variant="ghost" onClick={() => onBlock(node)}>
                  <UserX data-icon="inline-start" />
                  Not for me
                </Button>
              )}
            </div>
          </div>
          <Songs artist={node.name} />
        </>
      )}
    </Sheet>
  )
}

function Songs({ artist }: { artist: string }) {
  const songs = useQuery(artistSongsQuery(artist))
  if (songs.isPending) {
    return (
      <div className="flex flex-col gap-2">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-14 rounded-2xl" />
        ))}
      </div>
    )
  }
  if (songs.isError) return <Notice>{errorMessage(songs.error)}</Notice>
  if (songs.data.items.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {songs.data.noGraph ? "This server doesn't know anyone's songs yet." : `Nothing of ${artist}'s you don't have turned up yet.`}
      </p>
    )
  }
  return (
    <ul className="-mx-2 flex flex-col" aria-label={`Songs by ${artist}`}>
      {songs.data.items.map((it) => (
        <SongRow key={it.key} item={it} />
      ))}
    </ul>
  )
}
