import { useQuery } from '@tanstack/react-query'
import { AnimatePresence, motion, type PanInfo } from 'motion/react'
import { useEffect, useRef } from 'react'
import { UserAvatar } from '@/components/user-avatar'
import { useMe } from '@/lib/auth'
import { chatOpen, chatPopup, chatPopupsMuted, dismissChatPopup, liveChat, popChat, popupTitle } from '@/lib/chat'
import { easeOutExpo } from '@/lib/motion'
import { useCurrentRoom } from '@/lib/room'
import { useStore } from '@/lib/store'
import { usersQuery } from '@/lib/users'

/** A swipe this far, in pixels, dismisses the popup. */
const SWIPE_PX = 60

/**
 * New chat messages, wherever you are in the app (MAD-844): a small glass
 * bubble near the top that leaves by itself. Tap it to open the chat,
 * swipe it away. Never for your own messages, while the chat is open, or
 * on a device that muted them. It has its own slot, so error toasts at
 * the bottom stay visible.
 */
export function ChatPopup() {
  const me = useMe()
  const { room } = useCurrentRoom()
  const live = useStore(liveChat)
  const open = useStore(chatOpen)
  const muted = useStore(chatPopupsMuted)
  const popup = useStore(chatPopup)
  const users = useQuery(usersQuery)

  // Only what arrives from now on: nothing replays when the shell mounts.
  const seen = useRef<Set<string> | null>(null)
  seen.current ??= new Set(live.map((m) => m.id))
  useEffect(() => {
    const fresh = live.filter((m) => !seen.current?.has(m.id))
    for (const m of fresh) {
      seen.current?.add(m.id)
      if (m.userId !== me.id && m.roomId === room?.id && !open && !muted) popChat(m)
    }
  }, [live, me.id, room?.id, open, muted])
  useEffect(() => {
    if (open) dismissChatPopup()
  }, [open])

  const latest = popup?.messages.at(-1)
  const sender = users.data?.find((u) => u.id === latest?.userId)
  const nameOf = (id: string) => users.data?.find((u) => u.id === id)?.displayName ?? 'Someone'
  // A swipe that springs back isn't a tap.
  const dragged = useRef(false)
  const onDragEnd = (_: unknown, info: PanInfo) => {
    setTimeout(() => (dragged.current = false))
    if (Math.abs(info.offset.x) > SWIPE_PX || info.offset.y < -SWIPE_PX / 2) dismissChatPopup()
  }

  return (
    <div aria-live="polite" className="pointer-events-none fixed inset-x-0 top-[calc(env(safe-area-inset-top)+0.75rem)] z-[60] flex justify-center px-3">
      <AnimatePresence>
        {popup && latest && (
          <motion.button
            key={popup.key}
            type="button"
            drag
            dragSnapToOrigin
            dragElastic={0.4}
            onDragStart={() => (dragged.current = true)}
            onDragEnd={onDragEnd}
            initial={{ opacity: 0, y: -12 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -8 }}
            transition={{ duration: 0.35, ease: easeOutExpo }}
            onClick={() => {
              if (dragged.current) return
              dismissChatPopup()
              chatOpen.set(true)
            }}
            aria-label={`Chat from ${popupTitle(popup.messages.map((m) => m.userId), nameOf)}: ${latest.body}. Open the chat`}
            className="glass-strong pointer-events-auto flex w-full max-w-sm touch-none items-start gap-2.5 rounded-2xl px-3 py-2.5 text-left shadow-float outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            {sender ? <UserAvatar user={sender} tooltip={false} className="size-8 shrink-0" /> : <span className="size-8 shrink-0 rounded-full bg-muted" />}
            <span className="min-w-0 flex-1">
              <span className="block truncate text-xs font-semibold">{popupTitle(popup.messages.map((m) => m.userId), nameOf)}</span>
              <span className="line-clamp-2 text-sm break-words text-muted-foreground">{latest.body}</span>
            </span>
          </motion.button>
        )}
      </AnimatePresence>
    </div>
  )
}
