-- name: SetPassword :exec
INSERT INTO credentials_password (user_id, hash, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET hash = excluded.hash, updated_at = excluded.updated_at;

-- name: GetPassword :one
SELECT * FROM credentials_password WHERE user_id = ?;

-- name: DeletePassword :exec
DELETE FROM credentials_password WHERE user_id = ?;

-- name: CreatePasskey :one
INSERT INTO credentials_passkey (id, user_id, name, data, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetPasskey :one
SELECT * FROM credentials_passkey WHERE id = ?;

-- name: ListPasskeys :many
SELECT * FROM credentials_passkey WHERE user_id = ? ORDER BY created_at;

-- UsePasskey saves the credential record after a sign-in (new sign count, flags).
-- name: UsePasskey :exec
UPDATE credentials_passkey SET data = ?, last_used_at = ? WHERE id = ?;

-- name: RenamePasskey :exec
UPDATE credentials_passkey SET name = ? WHERE id = ? AND user_id = ?;

-- name: DeletePasskey :exec
DELETE FROM credentials_passkey WHERE id = ? AND user_id = ?;

-- name: CreateSession :one
INSERT INTO sessions (token_hash, user_id, user_agent, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- GetSession returns an unexpired session.
-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash = sqlc.arg(token_hash) AND expires_at > sqlc.arg(now);

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?;

-- name: ListSessions :many
SELECT * FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= ?;

-- name: CountPasskeys :one
SELECT count(*) FROM credentials_passkey WHERE user_id = ?;

-- name: DeleteOtherSessions :exec
DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?;
