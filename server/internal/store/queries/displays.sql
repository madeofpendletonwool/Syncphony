-- name: CreateDisplay :one
INSERT INTO displays (id, token_hash, room_id, name, paired_by, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetDisplayByToken :one
SELECT * FROM displays WHERE token_hash = ? AND expires_at > sqlc.arg(now);

-- name: GetDisplay :one
SELECT * FROM displays WHERE id = ?;

-- name: ListDisplays :many
SELECT * FROM displays WHERE room_id = ? AND expires_at > sqlc.arg(now) ORDER BY created_at;

-- name: TouchDisplay :exec
UPDATE displays SET last_seen_at = ?, expires_at = ? WHERE id = ?;

-- name: DeleteDisplay :exec
DELETE FROM displays WHERE id = ?;

-- name: DeleteDisplayByToken :exec
DELETE FROM displays WHERE token_hash = ?;

-- name: DeleteExpiredDisplays :exec
DELETE FROM displays WHERE expires_at <= ?;
