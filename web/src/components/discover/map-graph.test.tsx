import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { DiscoveryMap, DiscoveryMapNode } from '@/lib/discover'
import { MapGraph } from './map-graph'

const node = (name: string, more: Partial<DiscoveryMapNode> = {}): DiscoveryMapNode => ({
  key: name.toLowerCase(),
  name,
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

const map = (...nodes: DiscoveryMapNode[]): DiscoveryMap => ({
  nodes,
  edges: nodes.slice(1).map((n) => ({ from: nodes[0].key, to: n.key, score: 0.6, sources: [] })),
  partial: false,
  noGraph: false,
  library: { links: 0, songs: 0, building: false },
})

afterEach(cleanup)

describe('MapGraph', () => {
  it('draws each artist, saying which you have, and picks one', () => {
    const onSelect = vi.fn()
    render(<MapGraph map={map(node('Duster', { center: true, hop: 0 }), node('Codeine', { owned: true }), node('Bedhead'))} onSelect={onSelect} />)
    expect(screen.getByRole('button', { name: 'Codeine, in your library' })).toBeTruthy()
    const bedhead = screen.getByRole('button', { name: 'Bedhead, new to you' })
    fireEvent.click(bedhead)
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ name: 'Bedhead' }))
    fireEvent.keyDown(screen.getByRole('button', { name: 'Duster, new to you' }), { key: 'Enter' })
    expect(onSelect).toHaveBeenLastCalledWith(expect.objectContaining({ name: 'Duster' }))
  })

  it('lays out a new map from the last', () => {
    const { rerender } = render(<MapGraph map={map(node('Duster', { center: true }), node('Low'))} onSelect={() => {}} />)
    rerender(<MapGraph map={map(node('Low', { center: true }), node('Duster'), node('Codeine'))} onSelect={() => {}} />)
    expect(screen.getAllByRole('button')).toHaveLength(3)
  })
})
