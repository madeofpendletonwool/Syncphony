import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { savedArtistsQuery, type SavedArtist } from '@/lib/saved-artists'
import { toasts } from '@/lib/toast'
import { SaveArtistButton } from './save-artist'

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn(), DELETE: vi.fn() }))
vi.mock('@/api/client', () => ({ api }))

const ok = (data: unknown, status = 200) => Promise.resolve({ data, response: new Response(null, { status }) })
const hum: SavedArtist = { id: 's1', name: 'Hum', key: 'hum', refs: [], savedAt: '2026-10-13T00:00:00Z' }

function renderButton(artists: Parameters<typeof SaveArtistButton>[0]['artists'], saved: SavedArtist[] = []) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(savedArtistsQuery.queryKey, saved)
  render(
    <QueryClientProvider client={client}>
      <SaveArtistButton artists={artists} />
    </QueryClientProvider>,
  )
  return client
}

beforeEach(() => {
  for (const f of Object.values(api)) f.mockReset()
  api.GET.mockReturnValue(new Promise(() => {}))
  toasts.set([])
})
afterEach(cleanup)

describe('SaveArtistButton', () => {
  it('saves a room song’s artist, and offers to undo', async () => {
    api.POST.mockReturnValue(ok(hum, 201))
    api.DELETE.mockReturnValue(ok(undefined, 204))
    const client = renderButton([{ name: 'Hum', itemId: 'i1', artistIndex: 0 }])
    fireEvent.click(screen.getByRole('button', { name: 'Save Hum to your Library' }))
    await waitFor(() => expect(client.getQueryData(savedArtistsQuery.queryKey)).toEqual([hum]))
    expect(api.POST).toHaveBeenCalledWith('/me/saved-artists', { body: { itemId: 'i1', artistIndex: 0 } })
    expect(screen.getByRole('button', { name: 'Remove Hum from your Library' }).getAttribute('aria-pressed')).toBe('true')

    const [t] = toasts.get()
    expect(t.message).toBe('Saved Hum to your Library')
    t.action!.onClick()
    await waitFor(() => expect(api.DELETE).toHaveBeenCalledWith('/me/saved-artists/{id}', { params: { path: { id: 's1' } } }))
  })

  it('knows an artist saved under another spelling', () => {
    renderButton([{ name: 'HUM' }], [hum])
    expect(screen.getByRole('button', { name: 'Remove HUM from your Library' })).toBeTruthy()
  })

  it('lets you pick from a song’s artists', async () => {
    renderButton([{ name: 'Hum' }, { name: 'Failure' }], [hum])
    fireEvent.click(screen.getByRole('button', { name: 'Save one of the artists to your Library' }))
    expect(await screen.findByRole('button', { name: 'Save Failure to your Library' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Remove Hum from your Library' })).toBeTruthy()
  })
})
