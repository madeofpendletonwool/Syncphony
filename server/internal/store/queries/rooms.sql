-- name: CreateRoom :one
INSERT INTO rooms (id, name, owner_id, fairness_mode, settings, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetRoom :one
SELECT * FROM rooms WHERE id = ?;

-- name: ListRooms :many
SELECT * FROM rooms ORDER BY created_at;

-- name: UpdateRoom :one
UPDATE rooms SET name = ?, fairness_mode = ?, settings = ?
WHERE id = ?
RETURNING *;

-- name: SetRoomPlayer :exec
UPDATE rooms SET player_device_id = ? WHERE id = ?;

-- name: DeleteRoom :exec
DELETE FROM rooms WHERE id = ?;
