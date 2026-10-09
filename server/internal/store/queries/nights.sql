-- name: AddHeart :exec
INSERT INTO hearts (queue_item_id, user_id, created_at) VALUES (?, ?, ?)
ON CONFLICT (queue_item_id, user_id) DO NOTHING;

-- name: RemoveHeart :exec
DELETE FROM hearts WHERE queue_item_id = ? AND user_id = ?;

-- name: ListHearts :many
SELECT user_id FROM hearts WHERE queue_item_id = ? ORDER BY created_at, user_id;

-- HeartCountsSince counts hearts on a room's songs that started playing
-- since a time.
-- name: HeartCountsSince :many
SELECT hearts.queue_item_id, count(*) AS hearts
FROM hearts
JOIN play_history ON play_history.queue_item_id = hearts.queue_item_id
WHERE play_history.room_id = sqlc.arg(room_id) AND play_history.started_at >= sqlc.arg(since)
GROUP BY hearts.queue_item_id;

-- PlaysSince returns a room's plays that started after a time, oldest
-- first, at most limit of the latest. ended_at is NULL for the one playing.
-- name: PlaysSince :many
SELECT * FROM (
    SELECT * FROM play_history
    WHERE room_id = sqlc.arg(room_id) AND started_at > sqlc.arg(since)
    ORDER BY started_at DESC, id DESC
    LIMIT sqlc.arg(limit)
) ORDER BY started_at, id;

-- FirstPlayOf is when an item first started playing.
-- name: FirstPlayOf :one
SELECT * FROM play_history WHERE queue_item_id = ? ORDER BY started_at LIMIT 1;

-- name: CreateNight :one
INSERT INTO nights (id, room_id, started_at, ended_at, ended_by, plays, queue_item_id, hearts)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: LastNight :one
SELECT * FROM nights WHERE room_id = ? ORDER BY ended_at DESC, id DESC LIMIT 1;

-- name: ListNights :many
SELECT * FROM nights WHERE room_id = ? ORDER BY ended_at DESC, id DESC LIMIT ?;

-- name: SetNightAwards :exec
UPDATE nights SET awards = ? WHERE id = ?;

-- name: SetNightBracket :exec
UPDATE nights SET bracket = ? WHERE id = ?;
