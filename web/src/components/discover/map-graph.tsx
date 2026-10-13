import { useReducedMotion } from 'motion/react'
import { useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import type { DiscoveryMap, DiscoveryMapNode } from '@/lib/discover'
import { between, betweenBox, layoutMap, nodeRadius, type Box, type Layout } from '@/lib/discover-map'
import { cn } from '@/lib/utils'

const tweenMs = 650

/**
 * The discovery map, drawn: artists as circles, alike ones close and
 * linked, bigger for better known. Ones you have are dimmed, new ones in
 * full color, seeds and the artist it's of ringed, and in a set's map the
 * ones near several seeds softly haloed. A changed map glides to its new
 * layout. Calm on purpose: no beat-reactive effects in the app shell.
 */
export function MapGraph({
  map,
  selected,
  onSelect,
  className,
}: {
  map: DiscoveryMap
  selected?: string
  onSelect: (n: DiscoveryMapNode) => void
  className?: string
}) {
  const reduced = useReducedMotion()
  const svg = useRef<SVGSVGElement>(null)
  // A new map is laid out from where the last one left its artists.
  const [laid, setLaid] = useState<{ map: DiscoveryMap; layout: Layout; from?: Layout }>(() => ({ map, layout: layoutMap(map) }))
  if (laid.map !== map) setLaid({ map, layout: layoutMap(map, laid.layout.at), from: laid.layout })
  const { layout, from } = laid
  const setMode = !!map.set

  // Tween from the last layout to this one, writing straight to the DOM
  // so sixty artists move without sixty re-renders a frame.
  useLayoutEffect(() => {
    const el = svg.current
    if (!el || !from || reduced) return
    const nodes = [...el.querySelectorAll<SVGGElement>('[data-node]')]
    const edges = [...el.querySelectorAll<SVGLineElement>('[data-edge]')]
    const draw = (t: number) => {
      const e = 1 - Math.pow(1 - t, 3)
      const box = betweenBox(from.box, layout.box, e)
      el.setAttribute('viewBox', viewBox(box))
      const at = (k: string) => between(from.at.get(k) ?? layout.at.get(k), layout.at.get(k) ?? { x: 0, y: 0 }, e)
      for (const n of nodes) {
        const p = at(n.dataset.node!)
        n.setAttribute('transform', `translate(${p.x} ${p.y})`)
        if (!from.at.has(n.dataset.node!)) n.style.opacity = String(e)
      }
      for (const l of edges) {
        const [a, b] = l.dataset.edge!.split('\u0000')
        const pa = at(a)
        const pb = at(b)
        l.setAttribute('x1', String(pa.x))
        l.setAttribute('y1', String(pa.y))
        l.setAttribute('x2', String(pb.x))
        l.setAttribute('y2', String(pb.y))
      }
    }
    let frame = 0
    const start = performance.now()
    const step = (now: number) => {
      const t = Math.min((now - start) / tweenMs, 1)
      draw(t)
      if (t < 1) frame = requestAnimationFrame(step)
      else for (const n of nodes) n.style.opacity = ''
    }
    draw(0)
    frame = requestAnimationFrame(step)
    return () => cancelAnimationFrame(frame)
  }, [layout, from, reduced])

  const near = useMemo(() => {
    const out = new Set<string>()
    if (!selected) return out
    for (const e of map.edges) {
      if (e.from === selected) out.add(e.to)
      if (e.to === selected) out.add(e.from)
    }
    return out
  }, [map.edges, selected])

  const pos = (k: string) => layout.at.get(k) ?? { x: 0, y: 0 }
  const key = (e: KeyboardEvent, n: DiscoveryMapNode) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      onSelect(n)
    }
  }

  return (
    <svg
      ref={svg}
      viewBox={viewBox(layout.box)}
      preserveAspectRatio="xMidYMid meet"
      role="group"
      aria-label="Map of artists"
      className={cn('block h-full w-full touch-manipulation select-none', className)}
    >
      <g className="text-muted-foreground">
        {map.edges.map((e) => {
          const a = pos(e.from)
          const b = pos(e.to)
          const lit = selected && (e.from === selected || e.to === selected)
          return (
            <line
              key={`${e.from}\u0000${e.to}`}
              data-edge={`${e.from}\u0000${e.to}`}
              x1={a.x}
              y1={a.y}
              x2={b.x}
              y2={b.y}
              stroke={lit ? 'var(--primary)' : 'currentColor'}
              strokeOpacity={lit ? 0.7 : selected ? 0.08 : 0.12 + 0.3 * e.score}
              strokeWidth={0.5 + 1.5 * e.score}
              strokeLinecap="round"
            />
          )
        })}
      </g>
      {map.nodes.map((n) => {
        const p = pos(n.key)
        const r = nodeRadius(n)
        const dim = selected && selected !== n.key && !near.has(n.key)
        const bridging = setMode && !n.seed && n.seeds >= 2
        return (
          <g
            key={n.key}
            data-node={n.key}
            transform={`translate(${p.x} ${p.y})`}
            role="button"
            tabIndex={0}
            aria-label={`${n.name}${n.owned ? ', in your library' : ', new to you'}${n.seed ? ', a seed' : ''}`}
            aria-pressed={selected === n.key}
            onClick={() => onSelect(n)}
            onKeyDown={(e) => key(e, n)}
            className="cursor-pointer outline-none [&:focus-visible>circle:first-of-type]:stroke-ring"
            style={{ transition: 'opacity 200ms', opacity: dim ? 0.35 : 1 }}
          >
            {/* A bigger target than the circle, for fingers. */}
            <circle r={Math.max(r + 6, 18)} fill="transparent" stroke="transparent" strokeWidth={3} />
            {bridging && <circle r={r + 9} fill="var(--primary)" fillOpacity={0.12} />}
            {(n.seed || n.center) && <circle r={r + 4} fill="none" stroke="var(--primary)" strokeWidth={2} strokeOpacity={0.9} />}
            <circle
              r={r}
              fill={n.owned ? 'var(--muted-foreground)' : 'var(--primary)'}
              fillOpacity={n.owned ? 0.3 : n.known ? 0.95 : 0.55}
              stroke={selected === n.key ? 'var(--foreground)' : 'var(--background)'}
              strokeWidth={selected === n.key ? 2 : 1.5}
              strokeDasharray={n.known ? undefined : '3 2'}
            />
            <text
              y={r + 12}
              textAnchor="middle"
              fontSize={n.center ? 13 : 11}
              fontWeight={n.center || n.seed ? 600 : 500}
              fill={n.owned ? 'var(--muted-foreground)' : 'var(--foreground)'}
              stroke="var(--background)"
              strokeWidth={3}
              strokeOpacity={0.8}
              paintOrder="stroke"
              strokeLinejoin="round"
            >
              {n.name.length > 22 ? `${n.name.slice(0, 21)}…` : n.name}
            </text>
          </g>
        )
      })}
    </svg>
  )
}

function viewBox(b: Box) {
  return `${b.x} ${b.y} ${b.w} ${b.h}`
}
