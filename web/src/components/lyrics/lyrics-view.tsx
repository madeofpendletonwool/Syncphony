import { useQuery } from '@tanstack/react-query'
import { Mic2, Minus, Music4, Plus, RotateCcw } from 'lucide-react'
import { motion, useReducedMotion } from 'motion/react'
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { Skeleton } from '@/components/ui/skeleton'
import { easeOutExpo } from '@/lib/motion'
import {
  activeLine,
  lineProgress,
  lyricsQuery,
  MAX_OFFSET_MS,
  OFFSET_STEP_MS,
  offsetKey,
  useLyricsOffset,
  type LyricLine,
} from '@/lib/lyrics'
import { positionAt, type NowPlaying } from '@/lib/now-playing'
import { useService } from '@/lib/services'
import { cn } from '@/lib/utils'

type Variant = 'panel' | 'sheet' | 'stage'

type Props = {
  np: NowPlaying
  /**
   * panel: a card in the room. sheet: the full-screen player. stage: the
   * big screen, huge and untouchable.
   */
  variant?: Variant
  /** Seeks to a tapped line, when the viewer may seek. */
  onSeek?: (positionMs: number) => void
  className?: string
}

// Sizes per variant: the current line is the biggest thing on the stage.
const lineClass: Record<Variant, string> = {
  panel: 'text-xl/snug font-semibold tracking-tight sm:text-2xl/snug',
  sheet: 'text-2xl/snug font-bold tracking-tight sm:text-3xl/snug',
  stage: 'text-[clamp(2rem,4.6vw,4.75rem)]/[1.12] font-bold tracking-tight',
}

/**
 * A song's lyrics, karaoke style: the line being sung lights up in the
 * album's color and stays in view. Plain lyrics just scroll. Everyone in
 * the room sees the same line, since it comes from the room's playback
 * position (corrected for this device's clock).
 */
export function LyricsView({ np, variant = 'panel', onSeek, className }: Props) {
  const { roomId, itemId } = np
  const lyrics = useQuery({ ...lyricsQuery(roomId ?? '', itemId ?? ''), enabled: !!roomId && !!itemId })
  const key = offsetKey(np.track)
  const [offset] = useLyricsOffset(key)

  if (!roomId || !itemId) return <LyricsEmpty variant={variant} title="No lyrics here" />
  if (lyrics.isPending) return <LyricsSkeleton variant={variant} className={className} />
  if (lyrics.isError) return <LyricsEmpty variant={variant} title="Couldn't load the lyrics" body="Try again in a moment." />
  const l = lyrics.data
  if (!l) return <LyricsEmpty variant={variant} title="No lyrics found" body="Nobody has written this one down yet." />
  if (l.instrumental)
    return <LyricsEmpty variant={variant} icon={Music4} title="Instrumental" body="No words, just vibes." />

  return (
    <div className={cn('relative flex min-h-0 flex-col', className)}>
      {l.synced ? (
        <SyncedLyrics lines={l.lines} np={np} offset={offset} variant={variant} onSeek={onSeek} />
      ) : (
        <PlainLyrics text={l.plain} variant={variant} np={np} />
      )}
      {variant !== 'stage' && (
        <div className="flex flex-wrap items-center justify-between gap-2 pt-3">
          <LyricsSource source={l.source} synced={l.synced} />
          {l.synced && <OffsetControl offsetKey={key} />}
        </div>
      )}
    </div>
  )
}

function SyncedLyrics({
  lines,
  np,
  offset,
  variant,
  onSeek,
}: {
  lines: LyricLine[]
  np: NowPlaying
  offset: number
  variant: Variant
  onSeek?: (positionMs: number) => void
}) {
  const position = () => positionAt(np, Math.max(Date.now(), np.at)) - offset
  const current = useActiveLine(lines, np, position)
  const scroller = useRef<HTMLDivElement>(null)
  const lineRefs = useRef<(HTMLElement | null)[]>([])
  const reduced = useReducedMotion()
  // Reading ahead pauses following along for a few seconds.
  const [userScrolled, setUserScrolled] = useState(0)
  const touched = () => variant !== 'stage' && setUserScrolled(Date.now())

  useLayoutEffect(() => {
    const box = scroller.current
    const el = lineRefs.current[Math.max(0, current)]
    if (!box || !el || Date.now() - userScrolled < 4000) return
    // The current line sits a bit above the middle, with the next ones below.
    const top = el.offsetTop - box.clientHeight * (variant === 'stage' ? 0.4 : 0.32) + el.offsetHeight / 2
    box.scrollTo({ top, behavior: reduced ? 'auto' : 'smooth' })
  }, [current, variant, reduced, userScrolled])

  return (
    <div
      ref={scroller}
      onWheel={touched}
      onTouchMove={touched}
      className={cn(
        'relative min-h-0 flex-1 overflow-y-auto overscroll-contain [scrollbar-width:none] [&::-webkit-scrollbar]:hidden',
        // Fade lines in and out at the edges.
        '[mask-image:linear-gradient(to_bottom,transparent,black_14%,black_80%,transparent)]',
        variant === 'stage' && 'pointer-events-none overflow-hidden',
      )}
    >
      <ol className={cn('flex flex-col py-[38%]', variant === 'stage' ? 'gap-[0.55em]' : 'gap-4')}>
        {lines.map((line, i) => {
          const state = i === current ? 'current' : i < current ? 'past' : 'future'
          const blank = line.text.trim() === ''
          return (
            <motion.li
              key={`${i}-${line.atMs}`}
              ref={(el) => {
                lineRefs.current[i] = el
              }}
              initial={false}
              animate={{
                opacity: state === 'current' ? 1 : state === 'past' ? 0.38 : 0.55,
                scale: state === 'current' ? 1 : 0.94,
                filter: variant === 'stage' && state !== 'current' && !reduced ? 'blur(1.5px)' : 'blur(0px)',
              }}
              transition={{ duration: 0.6, ease: easeOutExpo }}
              style={{ transformOrigin: 'left center' }}
              className={cn(lineClass[variant], 'text-balance')}
            >
              {blank ? (
                <Breath active={state === 'current'} variant={variant} />
              ) : onSeek ? (
                <button
                  type="button"
                  onClick={() => onSeek(line.atMs + offset)}
                  className="rounded-lg text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  <LineText line={line} lines={lines} i={i} state={state} np={np} position={position} />
                </button>
              ) : (
                <LineText line={line} lines={lines} i={i} state={state} np={np} position={position} />
              )}
            </motion.li>
          )
        })}
      </ol>
    </div>
  )
}

/**
 * A line's words. The current one fills with the album color as it's
 * sung, painted every frame without re-rendering React.
 */
function LineText({
  line,
  lines,
  i,
  state,
  np,
  position,
}: {
  line: LyricLine
  lines: LyricLine[]
  i: number
  state: 'past' | 'current' | 'future'
  np: NowPlaying
  position: () => number
}) {
  const ref = useRef<HTMLSpanElement>(null)
  const reduced = useReducedMotion()
  useEffect(() => {
    const el = ref.current
    if (!el || state !== 'current') return
    if (reduced) {
      el.style.setProperty('--fill', '100%')
      return
    }
    let raf = 0
    const paint = () => {
      const p = lineProgress(lines, i, position(), np.track.durationMs)
      el.style.setProperty('--fill', `${(p * 100).toFixed(1)}%`)
      if (!np.paused) raf = requestAnimationFrame(paint)
    }
    paint()
    return () => cancelAnimationFrame(raf)
    // position reads np, which this depends on.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state, lines, i, np, reduced])

  if (state !== 'current') {
    return <span className={state === 'past' ? 'text-foreground' : 'text-muted-foreground'}>{line.text}</span>
  }
  return (
    <span
      ref={ref}
      style={{
        backgroundImage:
          'linear-gradient(90deg, var(--pal-text) var(--fill, 0%), color-mix(in oklch, var(--pal-text) 55%, var(--foreground)) var(--fill, 0%))',
      }}
      className="bg-clip-text text-transparent [-webkit-box-decoration-break:clone] [box-decoration-break:clone] drop-shadow-[0_0_24px_color-mix(in_oklch,var(--pal-text)_35%,transparent)]"
    >
      {line.text}
    </span>
  )
}

/** Three dots that breathe through an instrumental break. */
function Breath({ active, variant }: { active: boolean; variant: Variant }) {
  return (
    <span aria-label="Instrumental break" className="inline-flex items-center gap-[0.35em] py-[0.2em] text-(--pal-text)">
      {[0, 1, 2].map((d) => (
        <motion.span
          key={d}
          animate={active ? { opacity: [0.3, 1, 0.3], scale: [0.8, 1.1, 0.8] } : { opacity: 0.35, scale: 0.8 }}
          transition={active ? { duration: 1.6, repeat: Infinity, delay: d * 0.25, ease: 'easeInOut' } : { duration: 0.3 }}
          className={cn('rounded-full bg-current', variant === 'stage' ? 'size-[0.32em]' : 'size-2')}
        />
      ))}
    </span>
  )
}

/**
 * The current line index, updated as the song plays: a timer set for the
 * next line's start, so it changes right on time without polling.
 */
function useActiveLine(lines: LyricLine[], np: NowPlaying, position: () => number) {
  const [current, setCurrent] = useState(() => activeLine(lines, position()))
  useEffect(() => {
    let timer = 0
    const tick = () => {
      const pos = position()
      const i = activeLine(lines, pos)
      setCurrent(i)
      const next = lines[i + 1]
      if (np.paused || !next) return
      timer = window.setTimeout(tick, Math.max(16, Math.min(next.atMs - pos + 5, 1000)))
    }
    tick()
    return () => window.clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- position reads np
  }, [lines, np])
  return current
}

/**
 * Lyrics without timings. On the big screen, where nobody can scroll,
 * they glide past at the song's pace.
 */
function PlainLyrics({ text, variant, np }: { text: string; variant: Variant; np: NowPlaying }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = ref.current
    if (!el || variant !== 'stage') return
    let raf = 0
    const glide = () => {
      const pos = positionAt(np, Math.max(Date.now(), np.at))
      el.scrollTop = (pos / Math.max(1, np.track.durationMs)) * (el.scrollHeight - el.clientHeight)
      if (!np.paused) raf = requestAnimationFrame(glide)
    }
    glide()
    return () => cancelAnimationFrame(raf)
  }, [np, variant])
  return (
    <div
      ref={ref}
      className={cn(
        'min-h-0 flex-1 overflow-y-auto overscroll-contain',
        variant === 'stage' &&
          'overflow-hidden [mask-image:linear-gradient(to_bottom,transparent,black_15%,black_85%,transparent)] [&>p]:py-[30%]',
      )}
    >
      <p
        className={cn(
          'whitespace-pre-line text-foreground/90',
          variant === 'stage' ? 'text-[clamp(1.5rem,2.6vw,2.5rem)]/[1.35] font-semibold' : 'text-lg/relaxed font-medium',
        )}
      >
        {text}
      </p>
    </div>
  )
}

function LyricsSource({ source, synced }: { source: string; synced: boolean }) {
  const service = useService(source)
  const from = source === 'lrclib' ? 'LRCLIB' : service.name
  return (
    <p className="text-caption text-muted-foreground">
      {synced ? 'Synced lyrics' : 'Lyrics'} from {from}
      {!synced && ' · not timed'}
    </p>
  )
}

/** Nudges this song's lyrics earlier or later, on this device. */
function OffsetControl({ offsetKey: key }: { offsetKey: string }) {
  const [offset, setOffset] = useLyricsOffset(key)
  const label = offset === 0 ? 'In sync' : `${offset > 0 ? '+' : '−'}${(Math.abs(offset) / 1000).toFixed(2)}s`
  return (
    <div className="flex items-center gap-1 rounded-full bg-muted p-0.5 text-caption" role="group" aria-label="Lyrics timing">
      <button
        type="button"
        onClick={() => setOffset(offset - OFFSET_STEP_MS)}
        disabled={offset <= -MAX_OFFSET_MS}
        aria-label="Show lyrics earlier"
        className="grid size-7 place-items-center rounded-full outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-40"
      >
        <Minus className="size-3.5" />
      </button>
      <span aria-live="polite" className="min-w-14 text-center font-medium tabular-nums">
        {label}
      </span>
      <button
        type="button"
        onClick={() => setOffset(offset + OFFSET_STEP_MS)}
        disabled={offset >= MAX_OFFSET_MS}
        aria-label="Show lyrics later"
        className="grid size-7 place-items-center rounded-full outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-40"
      >
        <Plus className="size-3.5" />
      </button>
      {offset !== 0 && (
        <button
          type="button"
          onClick={() => setOffset(0)}
          aria-label="Reset lyrics timing"
          className="grid size-7 place-items-center rounded-full outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <RotateCcw className="size-3.5" />
        </button>
      )}
    </div>
  )
}

function LyricsSkeleton({ variant, className }: { variant: Variant; className?: string }) {
  const h = variant === 'stage' ? 'h-14' : 'h-6'
  return (
    <div className={cn('flex flex-col justify-center gap-4 py-8', className)} aria-busy>
      {['w-3/4', 'w-1/2', 'w-2/3', 'w-2/5'].map((w) => (
        <Skeleton key={w} className={cn(h, w, 'rounded-xl')} />
      ))}
    </div>
  )
}

function LyricsEmpty({
  variant,
  icon: Icon = Mic2,
  title,
  body,
}: {
  variant: Variant
  icon?: typeof Mic2
  title: string
  body?: string
}) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-2 py-10 text-center">
      <span
        className={cn(
          'grid place-items-center rounded-2xl bg-(--pal-text)/12 text-(--pal-text)',
          variant === 'stage' ? 'size-20' : 'size-12',
        )}
      >
        <Icon className={variant === 'stage' ? 'size-10' : 'size-6'} />
      </span>
      <p className={variant === 'stage' ? 'text-display' : 'text-headline'}>{title}</p>
      {body && <p className={cn('text-muted-foreground', variant === 'stage' ? 'text-xl' : 'text-sm')}>{body}</p>}
    </div>
  )
}
