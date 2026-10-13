import { describe, expect, it } from 'vitest'
import type { DiscoveryMapNode } from './discover'
import { crumbs, layoutMap, nodeRadius } from './discover-map'

const node = (key: string, more: Partial<DiscoveryMapNode> = {}): DiscoveryMapNode => ({
  key,
  name: key,
  tags: [],
  owned: false,
  seed: false,
  center: false,
  blocked: false,
  hop: 1,
  seeds: 1,
  score: 0.5,
  known: true,
  ...more,
})

const dist = (a: { x: number; y: number }, b: { x: number; y: number }) => Math.hypot(a.x - b.x, a.y - b.y)

describe('layoutMap', () => {
  const map = {
    nodes: [node('center', { center: true, hop: 0 }), node('close'), node('far'), node('other')],
    edges: [
      { from: 'center', to: 'close', score: 0.95, sources: [] },
      { from: 'center', to: 'far', score: 0.1, sources: [] },
      { from: 'far', to: 'other', score: 0.9, sources: [] },
    ],
  }

  it('keeps the artist a map is of in the middle, and alike artists close', () => {
    const { at, box } = layoutMap(map)
    expect(at.get('center')).toEqual({ x: 0, y: 0 })
    expect(dist(at.get('center')!, at.get('close')!)).toBeLessThan(dist(at.get('center')!, at.get('far')!))
    for (const p of at.values()) {
      expect(p.x).toBeGreaterThanOrEqual(box.x)
      expect(p.x).toBeLessThanOrEqual(box.x + box.w)
      expect(p.y).toBeGreaterThanOrEqual(box.y)
      expect(p.y).toBeLessThanOrEqual(box.y + box.h)
    }
  })

  it('starts artists where they were', () => {
    const first = layoutMap(map)
    const again = layoutMap(map, first.at, 0)
    expect(again.at.get('far')).toEqual(first.at.get('far'))
  })

  it('lays out nothing', () => {
    expect(layoutMap({ nodes: [], edges: [] }).at.size).toBe(0)
  })
})

describe('nodeRadius', () => {
  it('grows with fame', () => {
    expect(nodeRadius({ fans: 5_000_000 })).toBeGreaterThan(nodeRadius({ fans: 500 }))
    expect(nodeRadius({ center: true })).toBeGreaterThanOrEqual(16)
  })
})

describe('crumbs', () => {
  it('retraces the set and each re-center', () => {
    expect(crumbs('Moody', ['Slowdive', 'Lush'])).toEqual([
      { label: 'Moody', path: [] },
      { label: 'Slowdive', path: ['Slowdive'] },
      { label: 'Lush', path: ['Slowdive', 'Lush'] },
    ])
    expect(crumbs(undefined, ['Hum'])).toEqual([{ label: 'Hum', path: ['Hum'] }])
  })
})
