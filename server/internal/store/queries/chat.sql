-- name: InsertChatMessage :exec
INSERT INTO chat_messages (id, room_id, user_id, body, queue_item_id, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetChatMessage :one
SELECT * FROM chat_messages WHERE id = ? AND room_id = ?;

-- ListChatMessages pages a room's messages newest first: those before an
-- ID ('' for the newest), leaving out deleted ones.
-- name: ListChatMessages :many
SELECT * FROM chat_messages
WHERE room_id = sqlc.arg(room_id) AND deleted_at IS NULL
  AND (CAST(sqlc.arg(before) AS TEXT) = '' OR id < CAST(sqlc.arg(before) AS TEXT))
ORDER BY id DESC
LIMIT sqlc.arg(limit);

-- name: DeleteChatMessage :exec
UPDATE chat_messages SET body = '', deleted_at = ? WHERE id = ?;

-- AssignChatToNight gives a night that just ended the room's messages
-- that have none, up to its end.
-- name: AssignChatToNight :exec
UPDATE chat_messages SET night_id = sqlc.arg(night_id)
WHERE room_id = sqlc.arg(room_id) AND night_id IS NULL AND created_at <= sqlc.arg(ended_at);
