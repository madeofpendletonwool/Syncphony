import { Music2 } from 'lucide-react'
import { motion } from 'motion/react'
import { useState } from 'react'
import { spring } from '@/lib/motion'
import { cn } from '@/lib/utils'

type Props = {
  src?: string
  alt?: string
  /** Shared-element id, so artwork can fly between the mini and full player. */
  layoutId?: string
  className?: string
}

/** Album art with a soft placeholder for missing or broken images. */
export function Artwork({ src, alt = '', layoutId, className }: Props) {
  const [failed, setFailed] = useState<string>()
  const show = src && failed !== src

  return (
    <motion.div
      layoutId={layoutId}
      transition={spring}
      className={cn(
        'relative aspect-square shrink-0 overflow-hidden rounded-xl bg-muted shadow-float outline outline-1 -outline-offset-1 outline-white/10',
        className,
      )}
    >
      {show ? (
        <img
          src={src}
          alt={alt}
          draggable={false}
          onError={() => setFailed(src)}
          className="size-full object-cover"
        />
      ) : (
        <div className="grid size-full place-items-center bg-[radial-gradient(circle_at_30%_20%,color-mix(in_oklch,var(--primary),transparent_55%),transparent_70%)] text-muted-foreground">
          <Music2 className="size-[38%] max-w-12" strokeWidth={1.5} />
        </div>
      )}
    </motion.div>
  )
}
