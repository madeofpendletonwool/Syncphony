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

-- BumpQueueVersion records a queue change and returns the new version.
-- name: BumpQueueVersion :one
UPDATE rooms SET queue_version = queue_version + 1 WHERE id = ? RETURNING queue_version;

-- name: SetRoomOwner :one
UPDATE rooms SET owner_id = ? WHERE id = ?
RETURNING *;

-- TransferRooms hands every room one user owns to another, and returns them.
-- name: TransferRooms :many
UPDATE rooms SET owner_id = sqlc.arg(to_id) WHERE owner_id = sqlc.arg(from_id)
RETURNING *;
