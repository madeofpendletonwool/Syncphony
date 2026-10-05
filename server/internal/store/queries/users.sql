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
