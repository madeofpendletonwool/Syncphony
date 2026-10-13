import { ExternalLink, LoaderCircle, Pause, Play, ThumbsDown, ThumbsUp, UserX } from 'lucide-react'
import { motion } from 'motion/react'
import { Artwork } from '@/components/artwork'
import { AlbumLink, ArtistLink } from '@/components/linked-names'
import { Button } from '@/components/ui/button'
import { becauseText, imageUrl, togglePreview, usePreview, youtubeUrl, type DiscoveryItem } from '@/lib/discover'
import { fadeUp } from '@/lib/motion'
import { cn } from '@/lib/utils'

/**
 * A song you don't have yet: why it's here, its 30-second preview, a
 * YouTube search for the whole thing, and thumbs.
 */
export function FeedCard({
  item,
  onThumb,
  onBlock,
  thumbed,
}: {
  item: DiscoveryItem
  onThumb: (kind: 'up' | 'down') => void
  onBlock: () => void
  thumbed?: 'up'
}) {
  const p = usePreview(item.key)
  const canPlay = !!item.deezerId && !p.failed
  const why = becauseText(item)
  return (
    <motion.li variants={fadeUp} layout className="glass flex flex-col gap-3 rounded-3xl p-3 sm:flex-row sm:items-center">
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <button
          type="button"
          disabled={!canPlay}
          onClick={() => togglePreview(item)}
          aria-label={!canPlay ? `No preview of ${item.title}` : p.playing ? `Pause ${item.title}` : `Play a preview of ${item.title}`}
          className="group relative shrink-0 rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-default"
        >
          <Artwork src={imageUrl('cover', item.cover, 250)} className="size-16 rounded-xl shadow-none" />
          {canPlay && (
            <span
              className={cn(
                'absolute inset-0 grid place-items-center rounded-xl bg-black/35 text-white transition-opacity',
                p.playing || p.loading ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 [@media(hover:none)]:opacity-100',
              )}
            >
              {p.loading ? <LoaderCircle className="size-6 animate-spin" /> : p.playing ? <Pause className="size-6 fill-current" /> : <Play className="size-6 fill-current" />}
            </span>
          )}
          {(p.playing || p.progress > 0) && (
            <span className="absolute inset-x-1.5 bottom-1.5 h-1 overflow-hidden rounded-full bg-white/30">
              <span className="block h-full bg-white" style={{ width: `${Math.round(p.progress * 100)}%` }} />
            </span>
          )}
        </button>
        <div className="min-w-0 flex-1">
          <p className="truncate font-medium">{item.title}</p>
          <p className="truncate text-sm text-muted-foreground">
            <ArtistLink name={item.artist} />
            {item.album && (
              <>
                {' · '}
                <AlbumLink title={item.album} artist={item.artist} />
              </>
            )}
          </p>
          <p className="truncate text-caption text-primary">
            {why}
            {!item.deezerId && <span className="text-muted-foreground">{why ? ' · ' : ''}No preview</span>}
          </p>
        </div>
      </div>
      <div className="flex items-center justify-end gap-1">
        <Button asChild variant="glass" size="sm">
          <a href={youtubeUrl(item)} target="_blank" rel="noopener noreferrer" aria-label={`Open ${item.title} on YouTube`}>
            <ExternalLink data-icon="inline-start" />
            YouTube
          </a>
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`Thumbs up ${item.title}`}
          aria-pressed={thumbed === 'up'}
          onClick={() => onThumb('up')}
          className={cn(thumbed === 'up' && 'text-primary')}
        >
          <ThumbsUp className={cn(thumbed === 'up' && 'fill-current')} />
        </Button>
        <Button variant="ghost" size="icon-sm" aria-label={`Thumbs down ${item.title}`} onClick={() => onThumb('down')}>
          <ThumbsDown />
        </Button>
        <Button variant="ghost" size="icon-sm" aria-label={`${item.artist} isn't for me`} title={`${item.artist} isn't for me`} onClick={onBlock}>
          <UserX />
        </Button>
      </div>
    </motion.li>
  )
}
