import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { roomsQuery, type Room } from '@/lib/room'
import { savedArtistsQuery, type SavedArtist } from '@/lib/saved-artists'
import { usableLinksQuery } from '@/lib/services'
import { usersQuery } from '@/lib/users'
import { InRouter } from '@/test/router'
import { SavedArtists } from './saved-artists'

vi.mock('@/api/client', () => ({ api: { GET: vi.fn(() => new Promise(() => {})), POST: vi.fn(), DELETE: vi.fn() } }))

function renderList(saved: SavedArtist[]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(savedArtistsQuery.queryKey, saved)
  client.setQueryData(usableLinksQuery.queryKey, [{ id: 'mine' }] as never)
  client.setQueryData(roomsQuery.queryKey, [{ id: 'r1', name: 'Living room' } as Room])
  client.setQueryData(usersQuery.queryKey, [{ id: 'bob', displayName: 'Bob Smith' }] as never)
  render(
    <QueryClientProvider client={client}>
      <InRouter>
        <SavedArtists />
      </InRouter>
    </QueryClientProvider>,
  )
}

afterEach(cleanup)

describe('SavedArtists', () => {
  it('lists where each was heard, opening it on a service you can use', async () => {
    renderList([
      {
        id: 's1', name: 'Hum', key: 'hum', savedAt: new Date().toISOString(), roomId: 'r1', queuedBy: 'bob',
        refs: [{ linkId: 'theirs', id: 'x' }, { linkId: 'mine', id: 'ar7' }],
      },
    ])
    const link = await screen.findByRole('link', { name: /Hum/ })
    expect(link.getAttribute('href')).toBe('/artist/mine/ar7')
    expect(screen.getByText(/Living room · queued by Bob/)).toBeTruthy()
  })

  it('explains how to save one when there are none', async () => {
    renderList([])
    expect(await screen.findByText(/No artists yet/)).toBeTruthy()
  })
})
