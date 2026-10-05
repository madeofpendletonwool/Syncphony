-- name: CreateInvite :one
INSERT INTO invites (code, created_by, role, created_at, expires_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetInvite :one
SELECT * FROM invites WHERE code = ?;

-- name: ListInvites :many
SELECT * FROM invites ORDER BY created_at DESC;

-- Use RedeemInvite, which wraps this with friendlier types.
-- name: MarkInviteUsed :one
UPDATE invites SET used_by = sqlc.arg(user_id), used_at = sqlc.arg(used_at)
WHERE code = sqlc.arg(code) AND used_by IS NULL AND expires_at > sqlc.arg(now)
RETURNING *;

-- name: DeleteInvite :exec
DELETE FROM invites WHERE code = ?;
