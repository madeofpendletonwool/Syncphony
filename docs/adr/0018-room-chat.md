# ADR 0018: Room chat

- **Status:** accepted
- **Date:** 2026-10-14
- **Issues:** MAD-836 (Phase 13), stage 3: MAD-842..845

## Context

A room already has reactions: an emoji floats up the big screen and is gone. People in a room also want to say things ("who queued this?", "turn it up"), to everyone, wherever they are in the app, and read back what was said later. Phase 13 adds room chat: a panel in the room, a small popup on any screen, and messages on the TV.

Reactions aren't kept, so they needed no rules about storage, retention or deleting. Chat does.

## Decision

### Messages belong to a room, and are kept with its night

Migration 00033 adds `chat_messages`: the room, the sender, the text, the song that was playing when it was sent (`queue_item_id`, nullable), when, and `deleted_at`.

- **Nights.** A night isn't a row until it ends (ADR 0016), so a message can't name its night as it's sent. When a night ends, every message in the room not yet given a night, up to the end, is given this one (`night_id`). Messages sent before a night's first song belong to it too. Chat is read back by paging (`GET /rooms/{id}/messages?before=&limit=`, newest first), which runs across nights.
- **Not in the Wrapped.** The recap is about the music, and chat often isn't. A night's messages stay readable in the chat panel, and `night_id` is there for a recap or export that wants them later.
- **Kept until the room or the sender goes.** Messages are short (500 characters at most), so a busy room adds kilobytes a night. Nothing is pruned. Deleting the room deletes its chat. Deleting an account deletes its messages, guests' included.
- **The song.** Each message keeps the queue item playing when it was sent, and the API returns that song's snapshot with it, so the panel can show "during <song>" without another request. A song later deleted from the room leaves the message without one.

### Who can read, post and delete

- **Members and guests** of the room read and post. Guests are part of the night, and chat is how they say hi. **Paired displays** read (`listChatMessages` is a display operation) but never post.
- **Deleting.** Anyone can delete their own messages. The room's owner and server admins can delete anyone's: that's the moderation there is. A deleted message's text is cleared at once and it leaves every screen (`chat.deleted`). The row stays, so a page boundary never shifts under someone scrolling.
- **No other moderation.** No reports, mutes or word filters. Rooms are friends on a self-hosted server. Whoever runs the room can delete, and can remove someone from it.

### Plain text, limited like reactions

A message is plain text: trimmed, 1–500 characters (counted in runes), never empty or only whitespace. Links aren't made clickable, and nothing is rendered as HTML or Markdown.

Sending is rate limited per person with the same token bucket as reactions, with a smaller burst: 5 messages, then one every 2 seconds (`rate_limited`, with `Retry-After`).

### Realtime

`chat.message` (ChatMessage) and `chat.deleted` (`{roomId, id}`) go out on the room's topic, so every screen with the room open hears them: the room socket stays open on every screen of the app (`RoomLive`), and on the TV.

### Where it shows

- **The panel.** A Chat button on the room page (and a tap on a popup) opens it: a bottom sheet on a phone, a side panel on a computer. Unread counts show on the button and the Room tab. Read state is per device, kept in local storage: the newest message seen.
- **Popups anywhere.** A small glass bubble near the top, gone after about 5 seconds, never for your own messages or while the panel is open. A burst collapses into one bubble. Each device can mute them; the unread count still counts. They sit in their own slot, not the toaster's, so an error toast is never pushed out.
- **The big screen.** A room setting, `screens.chat`: `floating` (default) drifts messages up the screen as glass speech bubbles, like reactions; `log` keeps the last few in a corner column; `off` shows none. Bubbles stay in the side margins, clear of the lyrics and the now-playing title, and only a few show at once. The desktop visualizer floats them the same way.

## Consequences

- Chat adds a write per message, and an update per night end over that night's messages, both indexed by room.
- There are no DMs, threads, images, reactions to messages or push notifications. A message only reaches someone with the app open.
- A room's owner can read back everything said in it. That's said where chat starts: "Everyone in the room can read this".
