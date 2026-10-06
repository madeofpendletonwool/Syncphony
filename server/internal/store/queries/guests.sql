-- name: CreateGuestPass :one
INSERT INTO guest_passes (id, room_id, created_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetGuestPass :one
SELECT * FROM guest_passes WHERE id = ?;

-- CurrentGuestPass is a room's newest pass that's still good.
-- name: CurrentGuestPass :one
SELECT * FROM guest_passes
WHERE room_id = ? AND revoked_at IS NULL AND expires_at > sqlc.arg(now)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: RevokeGuestPasses :exec
UPDATE guest_passes SET revoked_at = sqlc.arg(now) WHERE room_id = sqlc.arg(room_id) AND revoked_at IS NULL;

-- name: DeleteOldGuestPasses :exec
DELETE FROM guest_passes WHERE guest_passes.expires_at <= sqlc.arg(before) AND NOT EXISTS (
    SELECT 1 FROM guests WHERE guests.pass_id = guest_passes.id AND guests.ended_at IS NULL
);

-- name: CreateGuest :one
INSERT INTO guests (user_id, room_id, pass_id, created_at, expires_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetGuest :one
SELECT * FROM guests WHERE user_id = ?;

-- name: ListAllGuests :many
SELECT * FROM guests;

-- CountPassGuests counts the guests who joined with a pass.
-- name: CountPassGuests :one
SELECT count(*) FROM guests WHERE pass_id = ?;

-- ListRoomGuests returns a room's guests who are still in, newest first.
-- name: ListRoomGuests :many
SELECT sqlc.embed(guests), sqlc.embed(users)
FROM guests JOIN users ON users.id = guests.user_id
WHERE guests.room_id = ? AND guests.ended_at IS NULL AND guests.expires_at > sqlc.arg(now)
ORDER BY guests.created_at DESC;

-- ListExpiredGuests returns guests whose time is up but who haven't been purged.
-- name: ListExpiredGuests :many
SELECT * FROM guests WHERE ended_at IS NULL AND expires_at <= sqlc.arg(now);

-- name: EndGuest :exec
UPDATE guests SET ended_at = sqlc.arg(now) WHERE user_id = sqlc.arg(user_id) AND ended_at IS NULL;

-- CountGuestSongs counts the songs a user added to a room themselves,
-- toward a guest's limit. Songs they took back don't count.
-- name: CountGuestSongs :one
SELECT count(*) FROM queue_items
WHERE room_id = ? AND added_by = ? AND autopilot IS NULL AND state != 'removed';

-- name: GetServerKey :one
SELECT key FROM server_keys WHERE name = ?;

-- name: CreateServerKey :exec
INSERT INTO server_keys (name, key, created_at) VALUES (?, ?, ?) ON CONFLICT (name) DO NOTHING;
