import { Link } from '@tanstack/react-router'
import { motion } from 'motion/react'
import { Artwork } from '@/components/artwork'
import { ProviderIcon } from '@/components/provider-icon'
import { artistNames, artworkUrl, type AlbumResult, type ArtistResult } from '@/lib/browse'
import { fadeUp } from '@/lib/motion'
import { cn } from '@/lib/utils'

export function AlbumCard({
  album,
  linkId,
  providerIcon,
  subtitle,
  className,
}: {
  album: AlbumResult
  linkId: string
  providerIcon?: string
  subtitle?: string
  className?: string
}) {
  return (
    <motion.div variants={fadeUp} className={cn('min-w-0', className)}>
      <Link
        to="/album/$linkId/$albumId"
        params={{ linkId, albumId: album.id }}
        className="group block rounded-2xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <div className="relative">
          <Artwork
            src={artworkUrl(linkId, album.artwork, 400)}
            className="w-full rounded-2xl transition-transform duration-300 ease-out-expo group-hover:scale-[1.02] group-active:scale-[0.98]"
          />
          {providerIcon && (
            <ProviderIcon icon={providerIcon} className="glass-strong absolute right-2 bottom-2 size-6 rounded-lg [&_svg]:size-3.5" />
          )}
        </div>
        <p className="mt-2 truncate text-sm font-medium">{album.title}</p>
        <p className="truncate text-caption text-muted-foreground">
          {subtitle ?? [artistNames(album.artists), album.year].filter(Boolean).join(' · ')}
        </p>
      </Link>
    </motion.div>
  )
}

export function ArtistCard({ artist, linkId, providerIcon }: { artist: ArtistResult; linkId: string; providerIcon?: string }) {
  return (
    <motion.div variants={fadeUp} className="min-w-0">
      <Link
        to="/artist/$linkId/$artistId"
        params={{ linkId, artistId: artist.id }}
        className="group flex flex-col items-center rounded-2xl text-center outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <div className="relative w-full">
          <Artwork
            src={artworkUrl(linkId, artist.artwork, 300)}
            className="w-full rounded-full transition-transform duration-300 ease-out-expo group-hover:scale-[1.03] group-active:scale-[0.98]"
          />
          {providerIcon && (
            <ProviderIcon icon={providerIcon} className="glass-strong absolute right-1 bottom-1 size-6 rounded-full [&_svg]:size-3.5" />
          )}
        </div>
        <p className="mt-2 w-full truncate text-sm font-medium">{artist.name}</p>
        <p className="text-caption text-muted-foreground">Artist</p>
      </Link>
    </motion.div>
  )
}
