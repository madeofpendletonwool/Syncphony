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

const song = (id: string, title: string, seedTitle: string, userId: string): Suggestion => ({
  track: { linkId: 'l1', provider: 'fake', trackId: id, title, artists: [{ name: 'Null Island' }], durationMs: 1000, explicit: false },
  because: { itemId: `item-${seedTitle}`, title: seedTitle, userId },
})

// lists maps a scope and source, "mine:queue" say, to the songs the
// server suggests for them.
function serve(lists: Record<string, Suggestion[]>) {
  GET.mockImplementation((path: string, init?: { params?: { query?: { scope?: string; source?: string } } }) => {
    if (path !== '/rooms/{roomId}/suggestions') return new Promise(() => {})
    const { scope = 'mine', source = 'history' } = init?.params?.query ?? {}
    const items = lists[`${scope}:${source}`] ?? []
    return Promise.resolve({ data: { scope, items }, response: new Response(null, { status: 200 }) })
  })
}

function renderVibe() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(meQuery.queryKey, { id: 'alice', displayName: 'Alice' } as Me)
  client.setQueryData(roomsQuery.queryKey, [{ id: 'r1', name: 'Living room' } as Room])
  client.setQueryData(queueQuery('r1').queryKey, { roomId: 'r1', version: 1, items: [], upNext: [] } as unknown as QueueSnapshot)
  client.setQueryData(usersQuery.queryKey, [{ id: 'bob', displayName: 'Bob' }] as never)
  client.setQueryData(providersQuery.queryKey, [])
  chooseRoom('r1')
  return render(
    <QueryClientProvider client={client}>
      <VibeSuggestions />
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
    serve({ 'mine:history': [song('t08', 'Longitude', 'Latitude', 'alice')] })
    renderVibe()
    expect(await screen.findByText('Longitude')).toBeTruthy()
    expect(screen.getByText(/Like Latitude/)).toBeTruthy()
    expect(calls()[0]).toMatchObject({ scope: 'mine', source: 'history', refresh: false })
  })

  it("switches to the group's vibe", async () => {
    serve({ 'mine:history': [], 'group:history': [song('t14', 'Tuning Fork', 'Concert Pitch', 'bob')] })
    renderVibe()
    expect(await screen.findByText(/Queue a few songs/)).toBeTruthy()
    fireEvent.click(screen.getByRole('radio', { name: 'Group vibe' }))
    expect(await screen.findByText('Tuning Fork')).toBeTruthy()
    expect(screen.getByText(/Like Concert Pitch, Bob's pick/)).toBeTruthy()
  })

  it("reads the vibe from what's queued", async () => {
    serve({ 'mine:history': [], 'mine:queue': [song('t21', 'Harmonic', 'Chord Shift', 'alice')] })
    renderVibe()
    expect(await screen.findByText(/Queue a few songs/)).toBeTruthy()
    fireEvent.click(screen.getByRole('radio', { name: 'Queue' }))
    expect(await screen.findByText('Harmonic')).toBeTruthy()
    expect(calls().at(-1)).toMatchObject({ scope: 'mine', source: 'queue' })
  })

  it('asks for a new list on shuffle', async () => {
    serve({ 'mine:history': [song('t08', 'Longitude', 'Latitude', 'alice')] })
    renderVibe()
    await screen.findByText('Longitude')
    fireEvent.click(screen.getByRole('button', { name: 'Shuffle suggestions' }))
    await waitFor(() => expect(calls().at(-1)).toMatchObject({ scope: 'mine', refresh: true }))
  })
})
