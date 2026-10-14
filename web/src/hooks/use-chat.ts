import { useInfiniteQuery } from '@tanstack/react-query'
import { useEffect, useMemo } from 'react'
import { chatOpen, chatQuery, chatRead, markChatRead, oldestFirst, unreadCount } from '@/lib/chat'
import { useStore } from '@/lib/store'

/**
 * Keeps the current room's chat loaded (RoomLive calls it, on every
 * screen), so unread counts are right wherever you are. A device that has
 * never read this room's chat starts with nothing unread, and an open
 * panel reads everything as it comes.
 */
export function useChatSync(roomId: string | undefined) {
  const chat = useInfiniteQuery({ ...chatQuery(roomId ?? ''), enabled: !!roomId })
  const open = useStore(chatOpen)
  const marks = useStore(chatRead)
  const newest = chat.data?.pages[0]?.[0]?.id
  useEffect(() => {
    if (!roomId || !chat.isSuccess) return
    // Nothing to mark yet: an empty chat's first message is unread.
    if (!marks[roomId]) markChatRead(roomId, newest ?? '0')
    else if (open && newest) markChatRead(roomId, newest)
  }, [roomId, chat.isSuccess, newest, open, marks])
}

/** How many of the room's messages you haven't read on this device. */
export function useChatUnread(roomId: string | undefined, me: string) {
  const chat = useInfiniteQuery({ ...chatQuery(roomId ?? ''), enabled: !!roomId })
  const marks = useStore(chatRead)
  const lastRead = roomId ? marks[roomId] : undefined
  return useMemo(() => unreadCount(oldestFirst(chat.data), lastRead, me), [chat.data, lastRead, me])
}
