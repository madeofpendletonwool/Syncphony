import { forceCollide, forceLink, forceManyBody, forceSimulation, forceX, forceY, type SimulationLinkDatum, type SimulationNodeDatum } from 'd3-force'
import type { DiscoveryMap, DiscoveryMapNode } from './discover'

// Laying out the discovery map (MAD-821): a force-directed graph where
// alike artists sit close, and well-known ones are bigger. It's worked
// out all at once, off screen, then drawn; the map tweens from the last
// layout to the next, so re-centering glides instead of jumping.

export type Point = { x: number; y: number }
export type Box = { x: number; y: number; w: number; h: number }
export type Layout = { at: Map<string, Point>; box: Box }

/** A node's radius: bigger for better known, and for the artist the map is of. */
export function nodeRadius(n: Partial<Pick<DiscoveryMapNode, 'fans' | 'center' | 'seed'>>) {
  const fame = n.fans ? Math.min(Math.log10(n.fans) / 7, 1) : 0.3
  const r = 6 + 12 * fame
  return n.center ? Math.max(r, 16) : n.seed ? Math.max(r, 11) : r
}

/** About how wide a label is, at the map's font size. */
function labelWidth(name: string) {
  return Math.min(name.length, 22) * 6.2
}

type SimNode = SimulationNodeDatum & { key: string; r: number; w: number }
type SimLink = SimulationLinkDatum<SimNode> & { score: number }

/**
 * Lays a map out. Artists that were on the last layout start where they
 * were, and new ones beside the artist they're most like, so a map that
 * changes a little moves a little. The artist a map is of sits in the
 * middle.
 */
export function layoutMap(map: Pick<DiscoveryMap, 'nodes' | 'edges'>, prev?: Map<string, Point>, ticks = 300): Layout {
  const nodes: SimNode[] = map.nodes.map((n) => ({ key: n.key, r: nodeRadius(n), w: labelWidth(n.name) }))
  const byKey = new Map(nodes.map((n) => [n.key, n]))
  const links: SimLink[] = map.edges
    .filter((e) => byKey.has(e.from) && byKey.has(e.to))
    .map((e) => ({ source: e.from, target: e.to, score: e.score }))

  // Where to start: where they were, else beside their nearest neighbor
  // that has a place, else on a ring by rank (golden angle).
  const nearest = new Map<string, { other: string; score: number }>()
  for (const e of map.edges) {
    for (const [a, b] of [
      [e.from, e.to],
      [e.to, e.from],
    ]) {
      const cur = nearest.get(a)
      if (prev?.has(b) && (!cur || e.score > cur.score)) nearest.set(a, { other: b, score: e.score })
    }
  }
  nodes.forEach((n, i) => {
    const was = prev?.get(n.key)
    const near = nearest.get(n.key)
    const by = near && prev?.get(near.other)
    const angle = i * 2.39996
    if (was) {
      n.x = was.x
      n.y = was.y
    } else if (by) {
      n.x = by.x + Math.cos(angle) * 30
      n.y = by.y + Math.sin(angle) * 30
    } else {
      const d = i === 0 ? 0 : 40 + 14 * Math.sqrt(i)
      n.x = Math.cos(angle) * d
      n.y = Math.sin(angle) * d
    }
  })
  const center = map.nodes.find((n) => n.center)
  if (center) {
    const c = byKey.get(center.key)!
    c.fx = 0
    c.fy = 0
  }

  const sim = forceSimulation(nodes)
    .force(
      'link',
      forceLink<SimNode, SimLink>(links)
        .id((n) => n.key)
        .distance((l) => 34 + 120 * (1 - l.score))
        .strength((l) => 0.15 + 0.5 * l.score),
    )
    .force('charge', forceManyBody<SimNode>().strength(-160).distanceMax(400))
    .force('collide', forceCollide<SimNode>((n) => Math.max(n.r + 4, n.w / 2.4)).iterations(2))
    .force('x', forceX(0).strength(0.05))
    .force('y', forceY(0).strength(0.05))
    .stop()
  // A map that only moved a little settles quicker.
  sim.alpha(prev && prev.size > 0 ? 0.5 : 1)
  for (let i = 0; i < ticks; i++) sim.tick()

  const at = new Map<string, Point>()
  let x0 = Infinity
  let y0 = Infinity
  let x1 = -Infinity
  let y1 = -Infinity
  for (const n of nodes) {
    const x = n.x ?? 0
    const y = n.y ?? 0
    at.set(n.key, { x, y })
    x0 = Math.min(x0, x - Math.max(n.r, n.w / 2))
    x1 = Math.max(x1, x + Math.max(n.r, n.w / 2))
    y0 = Math.min(y0, y - n.r)
    y1 = Math.max(y1, y + n.r + 16) // the label below
  }
  if (nodes.length === 0) return { at, box: { x: -100, y: -100, w: 200, h: 200 } }
  const pad = 12
  // Never so tight that one artist fills the screen.
  const w = Math.max(x1 - x0 + 2 * pad, 240)
  const h = Math.max(y1 - y0 + 2 * pad, 240)
  return { at, box: { x: (x0 + x1) / 2 - w / 2, y: (y0 + y1) / 2 - h / 2, w, h } }
}

/** Partway from a to b, t from 0 to 1. */
export function between(a: Point | undefined, b: Point, t: number): Point {
  if (!a) return b
  return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t }
}

export function betweenBox(a: Box, b: Box, t: number): Box {
  return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t, w: a.w + (b.w - a.w) * t, h: a.h + (b.h - a.h) * t }
}

/** "Moody post-rock › Slowdive › Lush": where you've been on the map. */
export function crumbs(setName: string | undefined, path: readonly string[]) {
  const out: { label: string; path: string[] }[] = []
  if (setName) out.push({ label: setName, path: [] })
  path.forEach((name, i) => out.push({ label: name, path: path.slice(0, i + 1) }))
  return out
}
