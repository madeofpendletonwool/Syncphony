import { infiniteQueryOptions, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { components } from '@/api/schema.gen'
import { createStore } from './store'

// Room chat (ADR 0018): a panel in the room, popups anywhere in the app,
// and messages floating by on the big screen.

export type ChatMessage = components['schemas']['ChatMessage']
export type ChatLook = NonNullable<components['schemas']['RoomScreens']['chat']>

/** The longest message, in characters, as the server counts them. */
export const MAX_CHAT = 500

const PAGE = 50

/** A room's chat, a page at a time, newest first: pages[0][0] is the newest message. */
export const chatQuery = (roomId: string) =>
  infiniteQueryOptions({
    queryKey: ['chat', roomId],
    queryFn: ({ pageParam }) =>
      unwrap(api.GET('/rooms/{roomId}/messages', { params: { path: { roomId }, query: { limit: PAGE, before: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => (last.length < PAGE ? undefined : last.at(-1)?.id),
    // The socket keeps it current.
    staleTime: Infinity,
  })

export function sendChat(roomId: string, body: string) {
  return unwrap(api.POST('/rooms/{roomId}/messages', { params: { path: { roomId } }, body: { body } }))
}

export function deleteChat(roomId: string, messageId: string) {
  return unwrap(api.DELETE('/rooms/{roomId}/messages/{messageId}', { params: { path: { roomId, messageId } } }))
}

type Pages = InfiniteData<ChatMessage[], unknown>

/** The loaded messages, oldest first. */
export function oldestFirst(data: Pages | undefined): ChatMessage[] {
  return data ? data.pages.flat().reverse() : []
}

/** Messages that arrived over the socket, newest last: what popups and the big screen show. */
export const liveChat = createStore<ChatMessage[]>([])

/** A message from the socket: into the room's chat, and onto the screens that show it as it comes. */
export function putChatMessage(queryClient: QueryClient, roomId: string, m: ChatMessage) {
  queryClient.setQueryData(chatQuery(roomId).queryKey, (old: Pages | undefined) => {
    if (!old || old.pages.flat().some((x) => x.id === m.id)) return old
    return { ...old, pages: [[m, ...(old.pages[0] ?? [])], ...old.pages.slice(1)] }
  })
  liveChat.set((ms) => (ms.some((x) => x.id === m.id) ? ms : [...ms, m].slice(-40)))
}

/** A deleted message leaves everywhere it shows. */
export function dropChatMessage(queryClient: QueryClient, roomId: string, id: string) {
  queryClient.setQueryData(chatQuery(roomId).queryKey, (old: Pages | undefined) =>
    old ? { ...old, pages: old.pages.map((p) => p.filter((m) => m.id !== id)) } : old,
  )
  liveChat.set((ms) => ms.filter((m) => m.id !== id))
  chatPopup.set((p) => (p ? { ...p, messages: p.messages.filter((m) => m.id !== id) } : p))
}

// --- Grouping ---------------------------------------------------------------

/** Messages closer together than this, from one person, read as one group. */
const GROUP_MS = 5 * 60_000

export type ChatGroup = {
  userId: string
  messages: ChatMessage[]
  /** The song playing, shown when it changed since the group before. */
  during?: ChatMessage
}

/** Groups messages (oldest first) by sender and time; a new song starts a new group. */
export function groupChat(messages: ChatMessage[]): ChatGroup[] {
  const out: ChatGroup[] = []
  let song: string | undefined
  for (const m of messages) {
    const last = out.at(-1)
    const prev = last?.messages.at(-1)
    const songChanged = !!m.itemId && m.itemId !== song
    if (m.itemId) song = m.itemId
    if (last && prev && !songChanged && last.userId === m.userId && Date.parse(m.createdAt) - Date.parse(prev.createdAt) < GROUP_MS) {
      last.messages.push(m)
    } else {
      out.push({ userId: m.userId, messages: [m], during: songChanged ? m : undefined })
    }
  }
  return out
}

// --- Reading ----------------------------------------------------------------

const READ_KEY = 'syncphony.chat.read'

function readMarks(): Record<string, string> {
  try {
    const v = JSON.parse(localStorage.getItem(READ_KEY) ?? '{}') as unknown
    return v && typeof v === 'object' ? (v as Record<string, string>) : {}
  } catch {
    return {}
  }
}

/** The newest message read in each room, on this device. */
export const chatRead = createStore<Record<string, string>>(readMarks())

/** Marks a room's chat read up to a message. IDs are time-ordered, so later is greater. */
export function markChatRead(roomId: string, id: string) {
  const marks = chatRead.get()
  if (marks[roomId] && marks[roomId] >= id) return
  chatRead.set({ ...marks, [roomId]: id })
  try {
    localStorage.setItem(READ_KEY, JSON.stringify(chatRead.get()))
  } catch {
    // Private mode: lasts until reload.
  }
}

/** Messages newer than the last read, not counting your own. */
export function unreadCount(messages: ChatMessage[], lastRead: string | undefined, me: string) {
  if (!lastRead) return 0
  return messages.filter((m) => m.id > lastRead && m.userId !== me).length
}

/** Unread counts past this show as "50+". */
export const UNREAD_SHOWN = 50

export const unreadLabel = (n: number) => (n > UNREAD_SHOWN ? `${UNREAD_SHOWN}+` : String(n))

// --- The panel and popups ---------------------------------------------------

/** Whether the chat panel is open. Opened from the room, or by tapping a popup. */
export const chatOpen = createStore(false)

/** The chat popup showing: one bubble, collapsing a burst of messages. */
export type ChatPopup = { key: number; messages: ChatMessage[] }

export const chatPopup = createStore<ChatPopup | null>(null)

/** How long a popup stays up after its latest message. */
export const POPUP_MS = 5000

let popupTimer: ReturnType<typeof setTimeout> | undefined
let popupKey = 1

/** Shows a message in the popup, joining one that's still up. */
export function popChat(m: ChatMessage) {
  chatPopup.set((p) => (p ? { ...p, messages: [...p.messages, m].slice(-10) } : { key: popupKey++, messages: [m] }))
  clearTimeout(popupTimer)
  popupTimer = setTimeout(dismissChatPopup, POPUP_MS)
}

export function dismissChatPopup() {
  clearTimeout(popupTimer)
  chatPopup.set(null)
}

/** "Sam", "Sam and Alex", "Sam and 2 others": who's in a popup, latest first. */
export function popupTitle(userIds: string[], nameOf: (id: string) => string) {
  const people = [...new Set([...userIds].reverse())]
  const first = nameOf(people[0] ?? '')
  if (people.length <= 1) return first
  if (people.length === 2) return `${first} and ${nameOf(people[1])}`
  return `${first} and ${people.length - 1} others`
}

const MUTE_KEY = 'syncphony.chat.popups-muted'

function readMuted() {
  try {
    return localStorage.getItem(MUTE_KEY) === '1'
  } catch {
    return false
  }
}

/** Whether this device keeps chat popups quiet. Unread counts still count. */
export const chatPopupsMuted = createStore<boolean>(readMuted())

export function setChatPopupsMuted(on: boolean) {
  chatPopupsMuted.set(on)
  if (on) dismissChatPopup()
  try {
    localStorage.setItem(MUTE_KEY, on ? '1' : '0')
  } catch {
    // Private mode: lasts until reload.
  }
}
