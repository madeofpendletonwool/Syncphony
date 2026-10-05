-- name: CreateServiceLink :one
INSERT INTO service_links (id, user_id, provider, account_id, account_label, encrypted_credentials, created_at, updated_at, last_ok_at)
VALUES (?, ?, ?, ?, ?, ?, sqlc.arg(now), sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: GetServiceLink :one
SELECT * FROM service_links WHERE id = ?;

-- name: ListServiceLinks :many
SELECT * FROM service_links WHERE user_id = ? ORDER BY created_at;

-- UpdateServiceLinkCredentials saves re-sealed or rotated credentials and
-- marks the link healthy.
-- name: UpdateServiceLinkCredentials :exec
UPDATE service_links
SET encrypted_credentials = ?, status = 'ok', status_detail = '', updated_at = sqlc.arg(now), last_ok_at = sqlc.arg(now)
WHERE id = ?;

-- name: SetServiceLinkStatus :exec
UPDATE service_links SET status = ?, status_detail = ?, updated_at = ? WHERE id = ?;

-- name: MarkServiceLinkOK :exec
UPDATE service_links SET status = 'ok', status_detail = '', updated_at = sqlc.arg(now), last_ok_at = sqlc.arg(now) WHERE id = ?;

-- name: DeleteServiceLink :exec
DELETE FROM service_links WHERE id = ? AND user_id = ?;
