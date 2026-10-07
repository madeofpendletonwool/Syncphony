-- name: GetDJRoom :one
SELECT * FROM dj_rooms WHERE room_id = ?;

-- name: PutDJRoom :exec
INSERT INTO dj_rooms (room_id, nights, folded_through, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (room_id) DO UPDATE SET
    nights = excluded.nights, folded_through = excluded.folded_through, updated_at = excluded.updated_at;

-- name: ListDJAffinities :many
SELECT * FROM dj_affinities WHERE room_id = ? ORDER BY kind, key, member;

-- name: DeleteDJAffinities :exec
DELETE FROM dj_affinities WHERE room_id = ?;

-- name: AddDJAffinity :exec
INSERT INTO dj_affinities (room_id, kind, key, member, name, likes, skips, last_night, last_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- NightsAfter returns a room's nights that ended after a time, newest
-- first, at most limit.
-- name: NightsAfter :many
SELECT * FROM nights WHERE room_id = sqlc.arg(room_id) AND ended_at > sqlc.arg(after)
ORDER BY ended_at DESC, id DESC LIMIT sqlc.arg(limit);
