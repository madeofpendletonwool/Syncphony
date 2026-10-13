import { ExternalLink, LoaderCircle, Pause, Play } from 'lucide-react'
import { Artwork } from '@/components/artwork'
import { Button } from '@/components/ui/button'
import { imageUrl, togglePreview, usePreview, youtubeUrl, type DiscoveryItem } from '@/lib/discover'
import { cn } from '@/lib/utils'

/** A song in a list: its preview, and a YouTube search for the whole thing. */
export function SongRow({ item }: { item: DiscoveryItem }) {
  const p = usePreview(item.key)
  const canPlay = !!item.deezerId && !p.failed
  return (
    <li className="flex items-center gap-3 rounded-2xl p-2">
      <button
        type="button"
        disabled={!canPlay}
        onClick={() => togglePreview(item)}
        aria-label={!canPlay ? `No preview of ${item.title}` : p.playing ? `Pause ${item.title}` : `Play a preview of ${item.title}`}
        className="group relative shrink-0 rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-default"
      >
        <Artwork src={imageUrl('cover', item.cover, 120)} className="size-11 rounded-lg shadow-none" />
        {canPlay && (
          <span
            className={cn(
              'absolute inset-0 grid place-items-center rounded-lg bg-black/35 text-white transition-opacity',
              p.playing || p.loading ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 [@media(hover:none)]:opacity-100',
            )}
          >
            {p.loading ? <LoaderCircle className="size-4 animate-spin" /> : p.playing ? <Pause className="size-4 fill-current" /> : <Play className="size-4 fill-current" />}
          </span>
        )}
        {(p.playing || p.progress > 0) && (
          <span className="absolute inset-x-1 bottom-1 h-0.5 overflow-hidden rounded-full bg-white/30">
            <span className="block h-full bg-white" style={{ width: `${Math.round(p.progress * 100)}%` }} />
          </span>
        )}
      </button>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">{item.title}</p>
        <p className="truncate text-caption text-muted-foreground">{item.album ?? (canPlay ? '' : 'No preview')}</p>
      </div>
      <Button asChild variant="ghost" size="icon-sm">
        <a href={youtubeUrl(item)} target="_blank" rel="noopener noreferrer" aria-label={`Open ${item.title} on YouTube`} title="Open on YouTube">
          <ExternalLink />
        </a>
      </Button>
    </li>
  )
}
