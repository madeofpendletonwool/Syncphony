import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { components } from '@/api/schema.gen'
import { meQuery, type Me } from '@/lib/auth'
import { providersQuery } from '@/lib/services'
import { usersQuery } from '@/lib/users'
import { SourcePicker } from './source-picker'

vi.mock('@/api/client', () => ({ api: { GET: vi.fn(() => new Promise(() => {})) } }))

type ServiceLink = components['schemas']['ServiceLink']

const link = (id: string, provider: string, ownerId = 'alice'): ServiceLink => ({
  id,
  provider,
  ownerId,
  accountLabel: `${id} on music.example.com`,
  status: 'ok',
  shared: ownerId !== 'alice',
  createdAt: '2026-01-01T00:00:00Z',
})

function renderPicker(links: ServiceLink[], value?: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(meQuery.queryKey, { id: 'alice', displayName: 'Alice' } as Me)
  client.setQueryData(usersQuery.queryKey, [{ id: 'sam', displayName: 'Sam Vimes' }] as never)
  client.setQueryData(providersQuery.queryKey, [
    { id: 'navidrome', name: 'Navidrome', icon: 'navidrome' },
    { id: 'spotify', name: 'Spotify', icon: 'spotify' },
  ] as never)
  const onChange = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <SourcePicker links={links} value={value} onChange={onChange} />
    </QueryClientProvider>,
  )
  return onChange
}

const chip = (name: string) => screen.getByRole('radio', { name })

afterEach(cleanup)

describe('SourcePicker', () => {
  it('shows nothing with a single account', () => {
    renderPicker([link('nav', 'navidrome')])
    expect(screen.queryByRole('group', { name: 'Search in' })).toBeNull()
  })

  it('offers each account, naming shared ones and telling twins apart', () => {
    renderPicker([link('nav1', 'navidrome'), link('nav2', 'navidrome'), link('samnav', 'navidrome', 'sam'), link('spot', 'spotify')])
    expect(chip('All services').getAttribute('data-state')).toBe('on')
    expect(chip('Navidrome · nav1 on music.example.com')).toBeTruthy()
    expect(chip('Navidrome · nav2 on music.example.com')).toBeTruthy()
    expect(chip("Sam's Navidrome")).toBeTruthy()
    expect(chip('Spotify')).toBeTruthy()
  })

  it('picks an account, and all services again', () => {
    const onChange = renderPicker([link('nav', 'navidrome'), link('spot', 'spotify')], 'nav')
    expect(chip('Navidrome').getAttribute('data-state')).toBe('on')
    fireEvent.click(chip('Spotify'))
    expect(onChange).toHaveBeenLastCalledWith('spot')
    fireEvent.click(chip('All services'))
    expect(onChange).toHaveBeenLastCalledWith(undefined)
  })
})
