import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { Suspense, type ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { meQuery } from '@/lib/auth'
import { chatOpen, chatPopupsMuted, chatQuery, dismissChatPopup, liveChat, oldestFirst, putChatMessage, type ChatMessage } from '@/lib/chat'
import { chooseRoom, roomsQuery } from '@/lib/room'
import { usersQuery } from '@/lib/users'
import { InRouter } from '@/test/router'
import { ChatPanel } from './chat-panel'
import { ChatPopup } from './chat-popup'

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn(), DELETE: vi.fn() }))
vi.mock('@/api/client', () => ({ api }))

const ok = (data: unknown, status = 200) => Promise.resolve({ data, response: new Response(null, { status }) })

const users = [
  { id: 'me', displayName: 'Me', color: '#123456' },
  { id: 'sam', displayName: 'Sam', color: '#ff0000' },
  { id: 'alex', displayName: 'Alex', color: '#00ff00' },
  { id: 'kim', displayName: 'Kim', color: '#0000ff' },
]
const msg = (id: string, userId: string, body: string, itemId?: string): ChatMessage => ({
  id,
  roomId: 'r1',
  userId,
  body,
  createdAt: '2026-10-14T20:00:00Z',
  itemId,
  track: itemId ? ({ provider: 'fake', trackId: 't1', title: 'Glass Lung', artists: ['Hum'], artistIds: [], durationMs: 1, explicit: false } as ChatMessage['track']) : undefined,
})

function setup(ui: ReactNode, messages: ChatMessage[] = []) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  client.setQueryData(meQuery.queryKey, { id: 'me', role: 'member', displayName: 'Me' } as never)
  client.setQueryData(roomsQuery.queryKey, [{ id: 'r1', name: 'Living room', ownerId: 'sam' }] as never)
  client.setQueryData(usersQuery.queryKey, users as never)
  client.setQueryData(chatQuery('r1').queryKey, { pages: [[...messages].reverse()], pageParams: [undefined] })
  chooseRoom('r1')
  render(
    <QueryClientProvider client={client}>
      <Suspense>
        <InRouter>{ui}</InRouter>
      </Suspense>
    </QueryClientProvider>,
  )
  return client
}

beforeEach(() => {
  for (const f of Object.values(api)) f.mockReset()
  api.GET.mockReturnValue(new Promise(() => {}))
  chatOpen.set(false)
  chatPopupsMuted.set(false)
  liveChat.set([])
  dismissChatPopup()
})
afterEach(cleanup)

describe('ChatPanel', () => {
  it('shows the room’s messages, with the song they were sent during', async () => {
    chatOpen.set(true)
    setup(<ChatPanel />, [msg('1', 'sam', 'who queued this?', 'i1'), msg('2', 'me', 'me!', 'i1')])
    expect(await screen.findByText('who queued this?')).toBeTruthy()
    expect(screen.getByText('Glass Lung')).toBeTruthy()
    expect(screen.getByText('Sam')).toBeTruthy()
    expect(screen.getByText('Everyone in Living room can read this')).toBeTruthy()
    // Only your own messages can go, in someone else's room.
    expect(screen.getAllByRole('button', { name: 'Delete message' })).toHaveLength(1)
  })

  it('sends a message', async () => {
    chatOpen.set(true)
    const sent = msg('3', 'me', 'turn it up')
    api.POST.mockReturnValue(ok(sent, 201))
    const client = setup(<ChatPanel />)
    const box = await screen.findByLabelText('Message')
    const send = screen.getByRole('button', { name: 'Send' }) as HTMLButtonElement
    expect(send.disabled).toBe(true)
    fireEvent.change(box, { target: { value: '  turn it up ' } })
    fireEvent.click(send)
    await waitFor(() => expect(oldestFirst(client.getQueryData(chatQuery('r1').queryKey)).map((m) => m.id)).toEqual(['3']))
    expect(api.POST).toHaveBeenCalledWith('/rooms/{roomId}/messages', { params: { path: { roomId: 'r1' } }, body: { body: '  turn it up ' } })
    expect((box as HTMLTextAreaElement).value).toBe('')
  })

  it('deletes your own message', async () => {
    chatOpen.set(true)
    api.DELETE.mockReturnValue(ok(undefined, 204))
    const client = setup(<ChatPanel />, [msg('1', 'me', 'oops')])
    fireEvent.click(await screen.findByRole('button', { name: 'Delete message' }))
    await waitFor(() => expect(oldestFirst(client.getQueryData(chatQuery('r1').queryKey))).toEqual([]))
    expect(api.DELETE).toHaveBeenCalledWith('/rooms/{roomId}/messages/{messageId}', { params: { path: { roomId: 'r1', messageId: '1' } } })
  })
})

describe('ChatPopup', () => {
  it('pops up others’ messages, collapsed, and opens the chat', async () => {
    const client = setup(<ChatPopup />)
    await screen.findByText((_, el) => el?.getAttribute('aria-live') === 'polite')
    act(() => putChatMessage(client, 'r1', msg('1', 'me', 'mine')))
    expect(screen.queryByText('mine')).toBeNull()
    act(() => {
      putChatMessage(client, 'r1', msg('2', 'kim', 'hey'))
      putChatMessage(client, 'r1', msg('3', 'alex', 'yo'))
      putChatMessage(client, 'r1', msg('4', 'sam', 'this one slaps'))
    })
    expect(await screen.findByText('this one slaps')).toBeTruthy()
    expect(screen.getByText('Sam and 2 others')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Open the chat/ }))
    expect(chatOpen.get()).toBe(true)
  })

  it('stays quiet when muted, or while the chat is open', async () => {
    const client = setup(<ChatPopup />)
    await screen.findByText((_, el) => el?.getAttribute('aria-live') === 'polite')
    act(() => chatPopupsMuted.set(true))
    act(() => putChatMessage(client, 'r1', msg('1', 'sam', 'muted')))
    act(() => {
      chatPopupsMuted.set(false)
      chatOpen.set(true)
    })
    act(() => putChatMessage(client, 'r1', msg('2', 'sam', 'open')))
    expect(screen.queryByText('muted')).toBeNull()
    expect(screen.queryByText('open')).toBeNull()
  })
})
