-- Queries that repeat a sqlc.arg must name every parameter: sqlc numbers
-- repeated args (?N), and a plain ? after one would get the wrong index.

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
SET encrypted_credentials = sqlc.arg(encrypted_credentials), status = 'ok', status_detail = '', updated_at = sqlc.arg(now), last_ok_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: SetServiceLinkStatus :exec
UPDATE service_links SET status = ?, status_detail = ?, updated_at = ? WHERE id = ?;

-- name: MarkServiceLinkOK :exec
UPDATE service_links SET status = 'ok', status_detail = '', updated_at = sqlc.arg(now), last_ok_at = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- name: DeleteServiceLink :exec
DELETE FROM service_links WHERE id = ? AND user_id = ?;

-- name: GetUserServiceLink :one
SELECT * FROM service_links WHERE id = ? AND user_id = ?;

-- name: FindServiceLink :one
SELECT * FROM service_links WHERE user_id = ? AND provider = ? AND account_id = ?;

-- RelinkServiceLink stores fresh credentials from linking again.
-- name: RelinkServiceLink :exec
UPDATE service_links
SET encrypted_credentials = sqlc.arg(encrypted_credentials), account_label = sqlc.arg(account_label), status = 'ok', status_detail = '',
    updated_at = sqlc.arg(now), last_ok_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: ListAllServiceLinks :many
SELECT * FROM service_links ORDER BY id;

-- RewrapServiceLink replaces the ciphertext only (vault key rotation).
-- name: RewrapServiceLink :exec
UPDATE service_links SET encrypted_credentials = ? WHERE id = ?;

-- name: SetServiceLinkShared :exec
UPDATE service_links SET shared = ?, updated_at = ? WHERE id = ? AND user_id = ?;

-- ListUsableServiceLinks is every link user_id can search and queue from:
-- their own first, then everyone else's shared ones. (sqlc doesn't rewrite
-- sqlc.arg in ORDER BY, so the second use is ?1, the same parameter.)
-- name: ListUsableServiceLinks :many
SELECT * FROM service_links
WHERE user_id = sqlc.arg(user_id) OR shared
ORDER BY user_id != ?1, created_at;

-- GetUsableServiceLink is one link user_id may use: their own, or shared.
-- name: GetUsableServiceLink :one
SELECT * FROM service_links WHERE id = sqlc.arg(id) AND (user_id = sqlc.arg(user_id) OR shared);

-- ListMatchLinks returns the links a room may look for a song on: those
-- of the given users (who are in the room) and shared ones, unless
-- expired. The users' own links come first.
-- name: ListMatchLinks :many
SELECT * FROM service_links
WHERE status != 'expired' AND (shared OR user_id IN (sqlc.slice(user_ids)))
ORDER BY shared, created_at;
