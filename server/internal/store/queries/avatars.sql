-- name: PutAvatar :exec
INSERT INTO avatars (user_id, data, content_type, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET data = excluded.data, content_type = excluded.content_type, updated_at = excluded.updated_at;

-- name: GetAvatar :one
SELECT * FROM avatars WHERE user_id = ?;

-- name: DeleteAvatar :exec
DELETE FROM avatars WHERE user_id = ?;
