import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { usableLinksQuery } from '@/lib/services'
import { InRouter } from '@/test/router'
import { AlbumLink, ArtistLinks } from './linked-names'

const GET = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', () => ({ api: { GET, POST: vi.fn(), DELETE: vi.fn() } }))
const navigate = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-router', async (load) => ({
  ...(await load<typeof import('@tanstack/react-router')>()),
  useNavigate: () => navigate,
}))

const ok = (data: unknown) => Promise.resolve({ data, response: new Response(null, { status: 200 }) })
const missing = () => Promise.resolve({ error: { code: 'not_found', message: 'not found' }, response: new Response(null, { status: 404 }) })

function renderIn(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client.setQueryData(usableLinksQuery.queryKey, [{ id: 'mine' }] as never)
  render(
    <QueryClientProvider client={client}>
      <InRouter>{ui}</InRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  GET.mockReset()
  navigate.mockReset()
})
afterEach(cleanup)

describe('ArtistLinks', () => {
  it('links straight to an artist on a link you can use', async () => {
    renderIn(<ArtistLinks track={{ linkId: 'mine', artists: ['Null Island', 'Sine Language'], artistIds: ['ar2'] }} />)
    const direct = await screen.findByRole('link', { name: 'Null Island' })
    expect(direct.getAttribute('href')).toBe('/artist/mine/ar2')
    // No ID for the second: it's looked up, with a search as its href.
    expect(screen.getByRole('link', { name: 'Sine Language' }).getAttribute('href')).toContain('/search?')
    expect(GET.mock.calls.filter(([path]) => String(path).startsWith('/resolve'))).toEqual([])
  })

  it('looks up a name from someone else’s link when tapped', async () => {
    GET.mockReturnValue(ok({ linkId: 'mine', id: 'ar9' }))
    renderIn(<ArtistLinks track={{ linkId: 'theirs', artists: ['Null Island'], artistIds: ['x1'] }} />)
    fireEvent.click(await screen.findByRole('link', { name: 'Null Island' }))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/artist/$linkId/$artistId', params: { linkId: 'mine', artistId: 'ar9' } }))
    expect(GET).toHaveBeenCalledWith('/resolve/artist', { params: { query: { name: 'Null Island' } } })
  })
})

describe('AlbumLink', () => {
  it('searches for an album none of your services has', async () => {
    GET.mockReturnValue(missing())
    renderIn(<AlbumLink title="Zero Zero" artist="Null Island" />)
    fireEvent.click(await screen.findByRole('link', { name: 'Zero Zero' }))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: '/search', search: { q: 'Zero Zero', tab: 'albums' } }))
    expect(GET).toHaveBeenCalledWith('/resolve/album', { params: { query: { title: 'Zero Zero', artist: 'Null Island' } } })
  })
})
