import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { meQuery, type Me } from '@/lib/auth'
import { chooseRoom, queueQuery, roomsQuery, type QueueSnapshot, type Room } from '@/lib/room'
import { providersQuery } from '@/lib/services'
import type { Suggestion } from '@/lib/suggestions'
import { usersQuery } from '@/lib/users'
import { VibeSuggestions } from './vibe-suggestions'

const GET = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', () => ({ api: { GET, POST: vi.fn(), DELETE: vi.fn() } }))

const song = (id: string, title: string, seedTitle: string, userId: string, linkId = 'l1'): Suggestion => ({
  track: { linkId, provider: 'fake', trackId: id, title, artists: [{ name: 'Null Island' }], durationMs: 1000, explicit: false },
  because: { itemId: `item-${seedTitle}`, title: seedTitle, userId },
})

// lists maps a scope to the songs the server suggests for it.
function serve(lists: Record<string, Suggestion[]>) {
  GET.mockImplementation((path: string, init?: { params?: { query?: { scope: string } } }) => {
    if (path !== '/rooms/{roomId}/suggestions') return new Promise(() => {})
    const scope = init?.params?.query?.scope ?? 'mine'
    return Promise.resolve({ data: { scope, items: lists[scope] ?? [] }, response: new Response(null, { status: 200 }) })
  })
}

function renderVibe(source?: { linkId: string; name: string }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(meQuery.queryKey, { id: 'alice', displayName: 'Alice' } as Me)
  client.setQueryData(roomsQuery.queryKey, [{ id: 'r1', name: 'Living room' } as Room])
  client.setQueryData(queueQuery('r1').queryKey, { roomId: 'r1', version: 1, items: [], upNext: [] } as unknown as QueueSnapshot)
  client.setQueryData(usersQuery.queryKey, [{ id: 'bob', displayName: 'Bob' }] as never)
  client.setQueryData(providersQuery.queryKey, [])
  chooseRoom('r1')
  return render(
    <QueryClientProvider client={client}>
      <VibeSuggestions source={source} />
    </QueryClientProvider>,
  )
}

const calls = () => GET.mock.calls.filter(([path]) => path === '/rooms/{roomId}/suggestions').map(([, init]) => init.params.query)

beforeEach(() => {
  GET.mockReset()
})
afterEach(cleanup)

describe('VibeSuggestions', () => {
  it('shows your vibe first, with why each song is suggested', async () => {
    serve({ mine: [song('t08', 'Longitude', 'Latitude', 'alice')] })
    renderVibe()
    expect(await screen.findByText('Longitude')).toBeTruthy()
    expect(screen.getByText(/Like Latitude/)).toBeTruthy()
    expect(calls()[0]).toMatchObject({ scope: 'mine', refresh: false })
  })

  it("switches to the group's vibe", async () => {
    serve({ mine: [], group: [song('t14', 'Tuning Fork', 'Concert Pitch', 'bob')] })
    renderVibe()
    expect(await screen.findByText(/Queue a few songs/)).toBeTruthy()
    fireEvent.click(screen.getByRole('radio', { name: 'Group vibe' }))
    expect(await screen.findByText('Tuning Fork')).toBeTruthy()
    expect(screen.getByText(/Like Concert Pitch, Bob's pick/)).toBeTruthy()
  })

  it('asks for a new list on shuffle', async () => {
    serve({ mine: [song('t08', 'Longitude', 'Latitude', 'alice')] })
    renderVibe()
    await screen.findByText('Longitude')
    fireEvent.click(screen.getByRole('button', { name: 'Shuffle suggestions' }))
    await waitFor(() => expect(calls().at(-1)).toMatchObject({ scope: 'mine', refresh: true }))
  })

  it('shows only the picked account’s songs', async () => {
    serve({ mine: [song('t08', 'Longitude', 'Latitude', 'alice', 'l1'), song('t09', 'Meridian', 'Latitude', 'alice', 'l2')] })
    renderVibe({ linkId: 'l2', name: 'Spotify' })
    expect(await screen.findByText('Meridian')).toBeTruthy()
    expect(screen.queryByText('Longitude')).toBeNull()
  })

  it('says so when none are on the picked account', async () => {
    serve({ mine: [song('t08', 'Longitude', 'Latitude', 'alice', 'l1')] })
    renderVibe({ linkId: 'l2', name: 'Spotify' })
    expect(await screen.findByText('None of these are on Spotify. Shuffle for more?')).toBeTruthy()
  })
})
