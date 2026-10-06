-- name: CreateResetLink :one
INSERT INTO reset_links (id, code_hash, user_id, created_by, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- GetResetLink returns an unexpired link.
-- name: GetResetLink :one
SELECT * FROM reset_links WHERE code_hash = sqlc.arg(code_hash) AND expires_at > sqlc.arg(now);

-- ListResetLinks returns unexpired links, newest first.
-- name: ListResetLinks :many
SELECT * FROM reset_links WHERE expires_at > sqlc.arg(now) ORDER BY created_at DESC;

-- name: DeleteResetLink :execrows
DELETE FROM reset_links WHERE id = ?;

-- name: DeleteUserResetLinks :execrows
DELETE FROM reset_links WHERE user_id = ?;

-- name: DeleteExpiredResetLinks :exec
DELETE FROM reset_links WHERE expires_at <= ?;

-- UseResetLink deletes a link if it's still good. 0 rows means it isn't.
-- name: UseResetLink :execrows
DELETE FROM reset_links WHERE id = sqlc.arg(id) AND expires_at > sqlc.arg(now);
