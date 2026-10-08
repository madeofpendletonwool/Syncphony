import { Link } from '@tanstack/react-router'
import { motion } from 'motion/react'
import { useEffect, useRef } from 'react'
import { AddButton } from '@/components/add-button'
import { Artwork } from '@/components/artwork'
import { ProviderIcon } from '@/components/provider-icon'
import type { LaneStatus } from '@/hooks/use-add-to-lane'
import { artistNames, artworkUrl, type TrackResult } from '@/lib/browse'
import { fadeUp } from '@/lib/motion'
import { formatDuration } from '@/lib/now-playing'
import { cn } from '@/lib/utils'

type Props = {
  track: TrackResult
  status: LaneStatus
  onAdd: () => void
  /** Provider icon to tag the row with, when results mix services. */
  providerIcon?: string
  /** Track number instead of artwork, for album track lists. */
  number?: number
  /** Hide the album name (on the album's own page). */
  hideAlbum?: boolean
  /** Shown after the artist instead of the album, like why it's suggested. */
  note?: string
  /** Highlighted from the keyboard, for Enter to add. */
  active?: boolean
}

/** A song in a list, with its one-tap add. */
export function TrackRow({ track, status, onAdd, providerIcon, number, hideAlbum, note, active }: Props) {
  const albumId = track.album?.id
  const ref = useRef<HTMLLIElement>(null)
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: 'nearest' })
  }, [active])
  return (
    <motion.li
      ref={ref}
      variants={fadeUp}
      className={cn('flex items-center gap-3 rounded-2xl py-1.5 pr-1', active && 'bg-foreground/8 ring-8 ring-foreground/8')}
    >
      {number !== undefined ? (
        <span className="w-6 shrink-0 text-center text-sm text-muted-foreground tabular-nums">{number}</span>
      ) : (
        <Artwork src={artworkUrl(track.linkId, track.artwork, 120)} className="size-12 rounded-lg shadow-none" />
      )}
      <div className="min-w-0 flex-1">
        <p className="flex items-center gap-1.5 truncate font-medium">
          <span className="truncate">{track.title}</span>
          {track.explicit && (
            <span aria-label="Explicit" className="shrink-0 rounded-[0.25rem] bg-muted-foreground/25 px-1 text-[0.6rem] leading-4 font-semibold text-muted-foreground">
              E
            </span>
          )}
        </p>
        <p className="flex items-center gap-1.5 truncate text-sm text-muted-foreground">
          {providerIcon && <ProviderIcon icon={providerIcon} className="size-4 rounded-[0.3rem] [&_svg]:size-2.5" />}
          <span className="truncate">
            {artistNames(track.artists)}
            {note && ` · ${note}`}
            {!note && !hideAlbum && track.album && (
              <>
                {' · '}
                {albumId ? (
                  <Link
                    to="/album/$linkId/$albumId"
                    params={{ linkId: track.linkId, albumId }}
                    className="hover:text-foreground hover:underline"
                  >
                    {track.album.title}
                  </Link>
                ) : (
                  track.album.title
                )}
              </>
            )}
          </span>
        </p>
      </div>
      <span className="hidden shrink-0 text-sm text-muted-foreground tabular-nums sm:block">
        {formatDuration(track.durationMs)}
      </span>
      <AddButton status={status} onAdd={onAdd} title={track.title} />
    </motion.li>
  )
}
