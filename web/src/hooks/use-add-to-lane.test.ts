import { describe, expect, it } from 'vitest'
import type { QueueSnapshot } from '@/lib/room'
import { lastAdded } from './use-add-to-lane'

const item = (id: string, addedBy: string, lanePosition: number, state = 'queued') =>
  ({ id, addedBy, lanePosition, state, addedAt: '', track: {} }) as QueueSnapshot['items'][number]

describe('lastAdded', () => {
  const snap = {
    roomId: 'r',
    version: 1,
    upNext: [],
    items: [item('p', 'me', 0, 'playing'), item('a', 'me', 1), item('x', 'you', 0), item('c', 'me', 3), item('b', 'me', 2)],
  } satisfies QueueSnapshot

  it('picks your newest queued songs', () => {
    expect(lastAdded(snap, 'me', 2)).toEqual(['b', 'c'])
  })
  it('never picks more than asked', () => {
    expect(lastAdded(snap, 'me', 0)).toEqual([])
    expect(lastAdded(snap, 'you', 5)).toEqual(['x'])
  })
})
