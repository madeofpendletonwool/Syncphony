-- name: CreateUser :one
INSERT INTO users (id, username, display_name, avatar, color, role, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: UpdateUserProfile :one
UPDATE users SET display_name = ?, avatar = ?, color = ?
WHERE id = ?
RETURNING *;

-- name: SetUserRole :exec
UPDATE users SET role = ? WHERE id = ?;

-- CountActiveAdmins counts admins who can still sign in.
-- name: CountActiveAdmins :one
SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL AND removed_at IS NULL;

-- OldestActiveAdmin is the longest-standing admin who can sign in, other
-- than the given user.
-- name: OldestActiveAdmin :one
SELECT * FROM users
WHERE role = 'admin' AND disabled_at IS NULL AND removed_at IS NULL AND id <> sqlc.arg(except_id)
ORDER BY created_at, id
LIMIT 1;

-- name: SetUserDisabled :exec
UPDATE users SET disabled_at = ? WHERE id = ?;

-- AnonymizeUser blanks a removed account: it keeps only its ID and color,
-- so the history it's in still shows a lane.
-- name: AnonymizeUser :exec
UPDATE users SET
    username = sqlc.arg(username), display_name = sqlc.arg(display_name), avatar = NULL, role = 'member',
    disabled_at = sqlc.arg(now), removed_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- DeleteUnusedInvitesBy revokes the invites someone made that nobody used.
-- name: DeleteUnusedInvitesBy :exec
DELETE FROM invites WHERE created_by = ? AND used_by IS NULL;

-- name: DeleteUserServiceLinks :exec
DELETE FROM service_links WHERE user_id = ?;

-- name: DeleteUserPasskeys :exec
DELETE FROM credentials_passkey WHERE user_id = ?;

-- name: AddUserAudit :exec
INSERT INTO user_audit (id, actor_id, actor_name, target_id, target_name, action, detail, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListUserAudit :many
SELECT * FROM user_audit ORDER BY created_at DESC, id DESC LIMIT ?;

-- DeleteUser deletes an account outright. Only for guests, whose songs
-- went with their room.
-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;
