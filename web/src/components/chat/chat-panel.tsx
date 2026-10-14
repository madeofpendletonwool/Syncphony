import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { useRouter } from '@tanstack/react-router'
import { ArrowDown, Music2, SendHorizontal, Trash2, X } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { Dialog } from 'radix-ui'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { errorMessage } from '@/api/errors'
import { ArtistLinks } from '@/components/linked-names'
import { Button } from '@/components/ui/button'
import { UserAvatar } from '@/components/user-avatar'
import { useVisualViewport } from '@/hooks/use-visual-viewport'
import { useMe } from '@/lib/auth'
import { chatOpen, chatQuery, deleteChat, dropChatMessage, groupChat, MAX_CHAT, oldestFirst, putChatMessage, sendChat, type ChatGroup, type ChatMessage } from '@/lib/chat'
import { laneStyle } from '@/lib/lane'
import { easeOutExpo } from '@/lib/motion'
import type { User } from '@/lib/now-playing'
import { useCurrentRoom, type Room } from '@/lib/room'
import { useStore } from '@/lib/store'
import { usersQuery } from '@/lib/users'
import { cn } from '@/lib/utils'

/** How near the bottom counts as at the bottom, in pixels. */
const STICK_PX = 48

/**
 * The room's chat (MAD-843): a sheet from the bottom on a phone, a panel
 * down the side on a computer. Mounted once in the app shell, so a popup
 * can open it from any screen.
 */
export function ChatPanel() {
  const open = useStore(chatOpen)
  const { room } = useCurrentRoom()
  const router = useRouter()
  // Following an artist's name out of the chat closes it, as now playing does.
  useEffect(
    () =>
      router.subscribe('onBeforeNavigate', (e) => {
        if (e.pathChanged) chatOpen.set(false)
      }),
    [router],
  )
  useEffect(() => {
    if (!room) chatOpen.set(false)
  }, [room])
  const vp = useVisualViewport(open)

  return (
    <Dialog.Root open={open && !!room} onOpenChange={chatOpen.set}>
      <AnimatePresence>
        {open && room && (
          <Dialog.Portal forceMount>
            <Dialog.Overlay asChild forceMount>
              <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} className="fixed inset-0 z-[70] bg-black/40 sm:bg-black/20" />
            </Dialog.Overlay>
            {/* Follows the visible part of the page, so the keyboard never covers the composer. */}
            <div
              style={{ top: vp.offsetTop, height: vp.height || undefined }}
              className="pointer-events-none fixed inset-x-0 z-[70] flex flex-col justify-end sm:inset-y-0 sm:top-0! sm:h-auto! sm:flex-row sm:justify-end"
            >
              <Dialog.Content asChild forceMount aria-describedby={undefined}>
                <motion.div
                  initial={{ opacity: 0, y: 24 }}
                  animate={{ opacity: 1, y: 0 }}
                  exit={{ opacity: 0, y: 24 }}
                  transition={{ duration: 0.35, ease: easeOutExpo }}
                  className="glass-strong pointer-events-auto flex h-[85%] max-h-full w-full flex-col overflow-hidden rounded-t-3xl shadow-float outline-none sm:m-3 sm:h-auto sm:w-[24rem] sm:rounded-3xl"
                >
                  <Panel room={room} />
                </motion.div>
              </Dialog.Content>
            </div>
          </Dialog.Portal>
        )}
      </AnimatePresence>
    </Dialog.Root>
  )
}

function Panel({ room }: { room: Room }) {
  const me = useMe()
  const chat = useInfiniteQuery(chatQuery(room.id))
  const users = useQuery(usersQuery)
  const messages = useMemo(() => oldestFirst(chat.data), [chat.data])
  const groups = useMemo(() => groupChat(messages), [messages])
  const host = me.role === 'admin' || room.ownerId === me.id
  const userById = (id: string) => users.data?.find((u) => u.id === id)

  // Stick to the bottom unless you've scrolled up; then a pill says what's new.
  const list = useRef<HTMLDivElement>(null)
  const atBottom = useRef(true)
  // The newest message seen at the bottom, as of the last scroll.
  const [seenTo, setSeenTo] = useState<string>()
  const fresh = seenTo ? messages.filter((m) => m.id > seenTo && m.userId !== me.id).length : 0
  const ends = useRef<{ first?: string; last?: string; height: number }>({ height: 0 })
  useLayoutEffect(() => {
    const el = list.current
    if (!el) return
    const first = messages[0]?.id
    const last = messages.at(-1)
    const prev = ends.current
    if (prev.last === undefined || atBottom.current || last?.userId === me.id) {
      el.scrollTop = el.scrollHeight
    } else if (first !== prev.first && last?.id === prev.last) {
      // Older messages loaded above: keep what you were reading in place.
      el.scrollTop += el.scrollHeight - prev.height
    }
    ends.current = { first, last: last?.id, height: el.scrollHeight }
  }, [messages, me.id])

  const onScroll = () => {
    const el = list.current
    if (!el) return
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < STICK_PX
    if (atBottom.current) setSeenTo(messages.at(-1)?.id)
    ends.current.height = el.scrollHeight
    // Near the top: the page before.
    if (el.scrollTop < 120 && chat.hasNextPage && !chat.isFetchingNextPage) void chat.fetchNextPage()
  }
  const toBottom = () => {
    const el = list.current
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
    setSeenTo(messages.at(-1)?.id)
  }

  return (
    <>
      <header className="flex items-center justify-between gap-3 border-b border-border/60 px-5 pt-4 pb-3">
        <div className="min-w-0">
          <Dialog.Title className="text-headline">Chat</Dialog.Title>
          <p className="truncate text-xs text-muted-foreground">Everyone in {room.name} can read this</p>
        </div>
        <Dialog.Close asChild>
          <Button size="icon-sm" variant="ghost" aria-label="Close the chat" className="-mr-1">
            <X />
          </Button>
        </Dialog.Close>
      </header>

      <div className="relative min-h-0 flex-1">
        <div ref={list} onScroll={onScroll} role="log" aria-label="Messages" className="h-full overflow-y-auto overscroll-contain px-4 py-3">
          {chat.isFetchingNextPage && <p className="py-2 text-center text-xs text-muted-foreground">Loading older messages…</p>}
          {chat.isSuccess && !chat.hasNextPage && messages.length > 0 && (
            <p className="py-2 text-center text-xs text-muted-foreground">The start of the chat</p>
          )}
          {chat.isSuccess && messages.length === 0 && (
            <p className="flex h-full items-center justify-center px-6 text-center text-sm text-muted-foreground">
              No messages yet. Say hi to the room.
            </p>
          )}
          {chat.isError && <p className="py-6 text-center text-sm text-destructive">{errorMessage(chat.error)}</p>}
          <ol className="flex flex-col gap-3">
            {groups.map((g) => (
              <Group key={g.messages[0].id} group={g} user={userById(g.userId)} mine={g.userId === me.id} canDelete={host || g.userId === me.id} roomId={room.id} />
            ))}
          </ol>
        </div>
        <AnimatePresence>
          {fresh > 0 && (
            <motion.button
              type="button"
              initial={{ opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: 8 }}
              onClick={toBottom}
              className="glass-strong absolute bottom-3 left-1/2 flex -translate-x-1/2 items-center gap-1.5 rounded-full px-3 py-1.5 text-xs font-medium shadow-float outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <ArrowDown className="size-3.5" />
              {fresh === 1 ? 'New message' : `${fresh} new messages`}
            </motion.button>
          )}
        </AnimatePresence>
      </div>

      <Composer roomId={room.id} onSent={toBottom} />
    </>
  )
}

function Group({ group, user, mine, canDelete, roomId }: { group: ChatGroup; user?: User; mine: boolean; canDelete: boolean; roomId: string }) {
  const first = group.messages[0]
  return (
    <li className="flex flex-col gap-1.5">
      {group.during?.track && (
        <p className="mx-auto flex max-w-full items-center gap-1.5 rounded-full bg-muted/70 px-2.5 py-1 text-[0.7rem] text-muted-foreground">
          <Music2 className="size-3 shrink-0" />
          <span className="min-w-0 truncate">
            during <span className="font-medium text-foreground">{group.during.track.title}</span>
            {' · '}
            <ArtistLinks track={group.during.track} />
          </span>
        </p>
      )}
      <div className={cn('flex items-end gap-2', mine && 'flex-row-reverse')}>
        {user ? <UserAvatar user={user} className="size-7 shrink-0" /> : <span className="size-7 shrink-0 rounded-full bg-muted" />}
        <div className={cn('flex min-w-0 flex-col gap-0.5', mine ? 'items-end' : 'items-start')}>
          <p className="flex items-baseline gap-1.5 px-1 text-[0.7rem]" style={laneStyle(user?.color)}>
            {!mine && <span className="font-semibold text-(--lane)">{user?.displayName ?? 'Someone'}</span>}
            <time dateTime={first.createdAt} className="text-muted-foreground">
              {new Date(first.createdAt).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}
            </time>
          </p>
          {group.messages.map((m) => (
            <Bubble key={m.id} m={m} mine={mine} color={user?.color} canDelete={canDelete} roomId={roomId} />
          ))}
        </div>
      </div>
    </li>
  )
}

/** Holding a message this long, on a touch screen, offers to delete it. */
const LONG_PRESS_MS = 500

function Bubble({ m, mine, color, canDelete, roomId }: { m: ChatMessage; mine: boolean; color?: string; canDelete: boolean; roomId: string }) {
  const queryClient = useQueryClient()
  const [armed, setArmed] = useState(false)
  const [error, setError] = useState<string>()
  const press = useRef<ReturnType<typeof setTimeout>>(undefined)
  const onPointerDown = (e: PointerEvent) => {
    if (!canDelete || e.pointerType === 'mouse') return
    press.current = setTimeout(() => setArmed(true), LONG_PRESS_MS)
  }
  const cancelPress = () => clearTimeout(press.current)
  // A long press shows the delete button for a moment.
  useEffect(() => {
    if (!armed) return
    const t = setTimeout(() => setArmed(false), 4000)
    return () => clearTimeout(t)
  }, [armed])
  const remove = () => {
    setError(undefined)
    deleteChat(roomId, m.id)
      .then(() => dropChatMessage(queryClient, roomId, m.id))
      .catch((e: unknown) => setError(errorMessage(e)))
  }

  return (
    <div className={cn('group flex max-w-[17rem] items-center gap-1 sm:max-w-[19rem]', mine && 'flex-row-reverse')}>
      <p
        onPointerDown={onPointerDown}
        onPointerUp={cancelPress}
        onPointerLeave={cancelPress}
        onPointerCancel={cancelPress}
        style={laneStyle(color)}
        className={cn(
          'min-w-0 rounded-2xl px-3 py-1.5 text-sm break-words whitespace-pre-wrap [-webkit-touch-callout:none] pointer-coarse:select-none',
          mine ? 'bg-primary text-primary-foreground' : 'bg-(--lane)/14',
        )}
      >
        {m.body}
      </p>
      {canDelete && (
        <Button
          size="icon-xs"
          variant="ghost"
          aria-label="Delete message"
          onClick={remove}
          className={cn('shrink-0 text-muted-foreground opacity-0 group-hover:opacity-100 focus-visible:opacity-100', armed && 'text-destructive opacity-100')}
        >
          <Trash2 />
        </Button>
      )}
      {error && <span className="text-xs text-destructive">{error}</span>}
    </div>
  )
}

const finePointer = () => typeof window !== 'undefined' && !!window.matchMedia?.('(pointer: fine)').matches

function Composer({ roomId, onSent }: { roomId: string; onSent: () => void }) {
  const queryClient = useQueryClient()
  const [body, setBody] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string>()
  const input = useRef<HTMLTextAreaElement>(null)
  const length = [...body.trim()].length
  const send = () => {
    if (length === 0 || length > MAX_CHAT || sending) return
    setSending(true)
    setError(undefined)
    sendChat(roomId, body)
      .then((m) => {
        putChatMessage(queryClient, roomId, m)
        setBody('')
        onSent()
        input.current?.focus()
      })
      .catch((e: unknown) => setError(errorMessage(e)))
      .finally(() => setSending(false))
  }
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // Enter sends on a computer; on a phone it's a new line, and the button sends.
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && finePointer()) {
      e.preventDefault()
      send()
    }
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        send()
      }}
      className="border-t border-border/60 px-3 pt-2.5 pb-[calc(env(safe-area-inset-bottom)+0.75rem)] sm:pb-3"
    >
      {error && (
        <p role="alert" className="px-2 pb-1.5 text-xs text-destructive">
          {error}
        </p>
      )}
      <div className="flex items-end gap-2">
        <label className="sr-only" htmlFor="chat-composer">
          Message
        </label>
        <textarea
          id="chat-composer"
          ref={input}
          rows={1}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Message the room"
          enterKeyHint="send"
          className="field-sizing-content max-h-32 min-h-10 flex-1 resize-none rounded-2xl bg-muted px-3.5 py-2.5 text-sm outline-none placeholder:text-muted-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        />
        <Button type="submit" size="icon" aria-label="Send" disabled={length === 0 || length > MAX_CHAT || sending} className="shrink-0 rounded-full">
          <SendHorizontal />
        </Button>
      </div>
      {length > MAX_CHAT - 50 && (
        <p className={cn('px-2 pt-1 text-right text-[0.7rem] tabular-nums', length > MAX_CHAT ? 'text-destructive' : 'text-muted-foreground')}>
          {length}/{MAX_CHAT}
        </p>
      )}
    </form>
  )
}
