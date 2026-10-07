-- name: SetRoomVisibility :one
UPDATE rooms SET visibility = ? WHERE id = ?
RETURNING *;

-- AddRoomMember lets someone into a room, or approves their request.
-- name: AddRoomMember :exec
INSERT INTO room_members (room_id, user_id, status, added_by, created_at)
VALUES (?, ?, 'member', ?, ?)
ON CONFLICT (room_id, user_id) DO UPDATE SET status = 'member', added_by = excluded.added_by, created_at = excluded.created_at
WHERE room_members.status = 'pending';

-- RequestRoomMembership asks to join; a member stays a member. It returns
-- how many rows it added: 0 if they'd already asked, or are in.
-- name: RequestRoomMembership :execrows
INSERT INTO room_members (room_id, user_id, status, created_at)
VALUES (?, ?, 'pending', ?)
ON CONFLICT (room_id, user_id) DO NOTHING;

-- name: GetRoomMember :one
SELECT * FROM room_members WHERE room_id = ? AND user_id = ?;

-- name: ListRoomMembers :many
SELECT * FROM room_members WHERE room_id = ? ORDER BY created_at, user_id;

-- name: RemoveRoomMember :execrows
DELETE FROM room_members WHERE room_id = ? AND user_id = ?;

-- MemberRoomIDs are the rooms someone is a member of (not just asked).
-- name: MemberRoomIDs :many
SELECT room_id FROM room_members WHERE user_id = ? AND status = 'member';

-- name: CreateRoomInvite :one
INSERT INTO room_invites (code, room_id, created_by, created_at, expires_at, max_uses)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetRoomInvite :one
SELECT * FROM room_invites WHERE code = ?;

-- ListRoomInvites lists a room's invites that still work, newest first.
-- name: ListRoomInvites :many
SELECT * FROM room_invites
WHERE room_id = sqlc.arg(room_id)
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now))
  AND (max_uses IS NULL OR uses < max_uses)
ORDER BY created_at DESC, code;

-- UseRoomInvite counts a use of an invite that still works.
-- name: UseRoomInvite :one
UPDATE room_invites SET uses = uses + 1
WHERE code = sqlc.arg(code)
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now))
  AND (max_uses IS NULL OR uses < max_uses)
RETURNING *;

-- name: DeleteRoomInvite :execrows
DELETE FROM room_invites WHERE room_id = ? AND code = ?;

-- name: DeleteRoomInvites :exec
DELETE FROM room_invites WHERE room_id = ?;

-- name: DeleteSpentRoomInvites :exec
DELETE FROM room_invites
WHERE (expires_at IS NOT NULL AND expires_at <= sqlc.arg(now))
   OR (max_uses IS NOT NULL AND uses >= max_uses);
