import { QueryClient } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  chatPopup,
  chatQuery,
  chatRead,
  dismissChatPopup,
  dropChatMessage,
  groupChat,
  liveChat,
  markChatRead,
  oldestFirst,
  popChat,
  popupTitle,
  putChatMessage,
  unreadCount,
  unreadLabel,
  type ChatMessage,
} from './chat'

const at = (min: number) => new Date(Date.UTC(2026, 9, 14, 20, min)).toISOString()
const msg = (id: string, userId: string, min: number, itemId?: string): ChatMessage => ({
  id,
  roomId: 'r1',
  userId,
  body: `message ${id}`,
  createdAt: at(min),
  itemId,
  track: itemId ? ({ title: `Song ${itemId}`, artists: ['A'] } as ChatMessage['track']) : undefined,
})

describe('groupChat', () => {
  it('groups one person’s messages close together', () => {
    const groups = groupChat([msg('1', 'sam', 0), msg('2', 'sam', 1), msg('3', 'alex', 2), msg('4', 'sam', 3), msg('5', 'sam', 20)])
    expect(groups.map((g) => g.messages.map((m) => m.id))).toEqual([['1', '2'], ['3'], ['4'], ['5']])
  })

  it('starts a group, with the song, when the song changes', () => {
    const groups = groupChat([msg('1', 'sam', 0, 'i1'), msg('2', 'sam', 1, 'i1'), msg('3', 'sam', 2, 'i2'), msg('4', 'sam', 3)])
    expect(groups.map((g) => [g.messages.map((m) => m.id), g.during?.itemId])).toEqual([
      [['1', '2'], 'i1'],
      [['3', '4'], 'i2'],
    ])
  })
})

describe('unreadCount', () => {
  const ms = [msg('1', 'sam', 0), msg('2', 'me', 1), msg('3', 'alex', 2)]
  it('counts others’ messages after the last read', () => {
    expect(unreadCount(ms, '1', 'me')).toBe(1)
    expect(unreadCount(ms, '0', 'me')).toBe(2)
    expect(unreadCount(ms, '3', 'me')).toBe(0)
  })
  it('counts nothing before a device has read anything', () => {
    expect(unreadCount(ms, undefined, 'me')).toBe(0)
  })
  it('caps the label', () => {
    expect(unreadLabel(3)).toBe('3')
    expect(unreadLabel(51)).toBe('50+')
  })
})

describe('markChatRead', () => {
  beforeEach(() => chatRead.set({}))
  it('only moves forward, and is remembered', () => {
    markChatRead('r1', '5')
    markChatRead('r1', '3')
    expect(chatRead.get()).toEqual({ r1: '5' })
    expect(JSON.parse(localStorage.getItem('syncphony.chat.read') ?? '{}')).toEqual({ r1: '5' })
  })
})

describe('the chat cache', () => {
  beforeEach(() => liveChat.set([]))
  it('adds live messages to the newest page once, and drops deleted ones everywhere', () => {
    const qc = new QueryClient()
    const key = chatQuery('r1').queryKey
    qc.setQueryData(key, { pages: [[msg('2', 'sam', 1), msg('1', 'sam', 0)]], pageParams: [undefined] })
    putChatMessage(qc, 'r1', msg('3', 'alex', 2))
    putChatMessage(qc, 'r1', msg('3', 'alex', 2))
    expect(oldestFirst(qc.getQueryData(key)).map((m) => m.id)).toEqual(['1', '2', '3'])
    expect(liveChat.get().map((m) => m.id)).toEqual(['3'])

    popChat(msg('3', 'alex', 2))
    dropChatMessage(qc, 'r1', '3')
    expect(oldestFirst(qc.getQueryData(key)).map((m) => m.id)).toEqual(['1', '2'])
    expect(liveChat.get()).toEqual([])
    expect(chatPopup.get()?.messages).toEqual([])
    dismissChatPopup()
  })
})

describe('popups', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {
    dismissChatPopup()
    vi.useRealTimers()
  })

  it('collapses a burst into one bubble, which leaves by itself', () => {
    popChat(msg('1', 'sam', 0))
    const key = chatPopup.get()?.key
    vi.advanceTimersByTime(3000)
    popChat(msg('2', 'alex', 0))
    expect(chatPopup.get()?.key).toBe(key)
    expect(chatPopup.get()?.messages).toHaveLength(2)
    vi.advanceTimersByTime(4999)
    expect(chatPopup.get()).not.toBeNull()
    vi.advanceTimersByTime(1)
    expect(chatPopup.get()).toBeNull()
  })

  it('names who’s talking, latest first', () => {
    const names: Record<string, string> = { sam: 'Sam', alex: 'Alex', kim: 'Kim' }
    const nameOf = (id: string) => names[id]
    expect(popupTitle(['sam'], nameOf)).toBe('Sam')
    expect(popupTitle(['alex', 'sam', 'sam'], nameOf)).toBe('Sam and Alex')
    expect(popupTitle(['kim', 'alex', 'sam'], nameOf)).toBe('Sam and 2 others')
  })
})
