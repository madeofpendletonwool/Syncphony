import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DiscoveryRelease } from '@/lib/discover'
import { ReleaseCard } from './release-card'

const GET = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', () => ({ api: { GET, POST: vi.fn(), DELETE: vi.fn() } }))

const inlet: DiscoveryRelease = {
  key: 'hum\u0000inlet',
  title: 'Inlet',
  artist: 'Hum',
  date: '2026-09-20',
  kind: 'album',
  deezerId: 10,
  sources: ['deezer'],
  because: ['Failure'],
  seed: false,
}

function renderCard(onBlock = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <ul>
        <ReleaseCard release={inlet} isNew onBlock={onBlock} />
      </ul>
    </QueryClientProvider>,
  )
  return onBlock
}

beforeEach(() => {
  GET.mockReset()
  GET.mockResolvedValue({
    data: { items: [{ key: 'hum\u0000waves', title: 'Waves', artist: 'Hum', album: 'Inlet', deezerId: 101, because: [], seed: false }] },
    response: new Response(null, { status: 200 }),
  })
})
afterEach(cleanup)

describe('ReleaseCard', () => {
  it('says what it is and why, and lists its songs when opened', async () => {
    const onBlock = renderCard()
    expect(screen.getByText('New')).toBeTruthy()
    expect(screen.getByText(/Album · Out/)).toBeTruthy()
    expect(screen.getByText(/Near Failure/)).toBeTruthy()
    expect(GET).not.toHaveBeenCalled()

    const open = screen.getByRole('button', { name: 'Songs' })
    fireEvent.click(open)
    expect(open.getAttribute('aria-expanded')).toBe('true')
    expect(await screen.findByRole('button', { name: 'Play a preview of Waves' })).toBeTruthy()
    expect(GET).toHaveBeenCalledWith(
      '/me/discover/release-tracks',
      expect.objectContaining({ params: { query: { artist: 'Hum', title: 'Inlet', deezerId: 10 } } }),
    )
    expect(screen.getByRole('link', { name: 'Open Inlet on YouTube' }).getAttribute('href')).toContain('Hum%20Inlet%20full%20album')

    fireEvent.click(screen.getByRole('button', { name: "Hum isn't for me" }))
    expect(onBlock).toHaveBeenCalled()
  })
})
