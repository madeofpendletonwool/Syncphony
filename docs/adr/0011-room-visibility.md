# ADR 0011: Who can see and join a room

- **Status:** accepted
- **Date:** 2026-10-07

## Context

Every account on a server could see every room and join it. That suits a household, but not a shared server where a few friends want their own room, or someone wants a room for tonight that doesn't clutter everyone's list. Guests (MAD-720) already belong to one room through their pass; members needed something similar.

## Decision

### Three levels

| Level | Listed for | Who gets in |
|---|---|---|
| **Open** (default, as before) | everyone on the server | any account |
| **Unlisted** | people who've joined | anyone with one of its invite links; any member can share one |
| **Private** | members | people the owner adds, or who use an invite only the owner (or an admin) can make, optionally needing the owner's approval |

The owner is always in. Guests stay in their pass's room whatever its level. Paired displays stay in their room.

`rooms.visibility` is a column (not part of the settings JSON) because listing and access checks filter on it. `approveJoins` is a room setting, and applies only while the room is private.

### Members and invites

- `room_members` (`room_id, user_id, status, added_by, created_at`) records who's in a room that isn't open. `status` is `member`, or `pending`: they used an invite to a private room that approves joins, and the owner hasn't let them in yet.
- `room_invites` are random codes (like server invites) with an optional expiry (up to 90 days) and an optional number of uses. Links are `/room-invite/<code>`, for signed-in members of the server only. Using one when you're already in uses nothing up.
- **Closing an open room** makes everyone who has it open, or has songs waiting, a member, so a party isn't cut off mid-song. The owner can trim the list afterwards.
- **Changing the level** revokes the room's invites, so a link someone shared for an unlisted room doesn't let anyone into it once it's private.
- **Handing a room over** keeps the old owner in as a member.
- **Removing someone** (or their leaving) takes their waiting songs out of the queue and closes their connections to the room with 4003.

### One check, everywhere

Every operation with `{roomId}` in its path goes through `enterRoom` in the auth middleware, and the room socket does the same check when it connects and on every ping. A room you can't open is **404, not 403**, so its existence doesn't leak. `TestRoomsOutsidersCantSee` walks the spec and calls every room operation as an outsider, so a new endpoint can't skip the check.

### Admins

Admins can change, hand over or delete any room (`adminRoomOps`), and see every room on the server page. But being an admin doesn't let them into a room that isn't open to them: they **join as an admin** (`POST /rooms/{roomId}/admin-join`), which makes them a member and tells everyone in the room. On the server page, what a room is playing is hidden until they do. An admin can read the database anyway, so this is about manners, not security.

## Consequences

- Someone added to a room, or let in, doesn't get a push: the room appears in their list the next time it's fetched (on focus, or within a minute). There's no per-user event channel outside a room yet.
- Members of an open room aren't recorded, so closing a room keeps only who's there now or has songs waiting, not everyone who ever visited.
- The big screen's join QR code (`/room?join=<id>`) only works for people who can already open the room. Private rooms share invite links instead.
- Any member of a private room can still start a guest pass, as in any room. A room that shouldn't take guests turns guests off.
