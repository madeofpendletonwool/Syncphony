import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { chatQuery, dropChatMessage, liveChat, putChatMessage, type ChatMessage } from '@/lib/chat'
import type { User } from '@/lib/now-playing'
import { BigScreenChat } from './big-screen-chat'

const users = [{ id: 'sam', displayName: 'Sam', color: '#ff0000' }] as User[]
const msg = (id: string, body: string, createdAt = new Date().toISOString()): ChatMessage => ({ id, roomId: 'r1', userId: 'sam', body, createdAt })

function setup(look: 'floating' | 'log' | 'off', messages: ChatMessage[] = []) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(chatQuery('r1').queryKey, { pages: [[...messages].reverse()], pageParams: [undefined] })
  render(
    <QueryClientProvider client={client}>
      <BigScreenChat roomId="r1" look={look} users={users} />
    </QueryClientProvider>,
  )
  return client
}

beforeEach(() => liveChat.set([]))
afterEach(cleanup)

describe('BigScreenChat', () => {
  it('floats new messages by with who sent them, and drops deleted ones', async () => {
    const client = setup('floating')
    act(() => putChatMessage(client, 'r1', msg('1', 'turn it up')))
    expect(screen.getByText('turn it up')).toBeTruthy()
    expect(screen.getByText('Sam')).toBeTruthy()
    act(() => dropChatMessage(client, 'r1', '1'))
    await waitFor(() => expect(screen.queryByText('turn it up')).toBeNull())
  })

  it('keeps the last few in a log, leaving out old ones', () => {
    const old = new Date(Date.now() - 60 * 60_000).toISOString()
    setup('log', [msg('1', 'an hour ago', old), msg('2', 'one'), msg('3', 'two'), msg('4', 'three'), msg('5', 'four'), msg('6', 'five')])
    expect(screen.queryByText('two')).toBeNull()
    expect(screen.getByText('five')).toBeTruthy()
    expect(screen.getByText('three')).toBeTruthy()
  })

  it('shows nothing when the room turned it off', () => {
    const client = setup('off')
    act(() => putChatMessage(client, 'r1', msg('1', 'hidden')))
    expect(screen.queryByText('hidden')).toBeNull()
  })
})
