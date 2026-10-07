-- name: GetServerSettings :one
SELECT settings FROM server_settings WHERE id = 1;

-- name: SetServerSettings :exec
INSERT INTO server_settings (id, settings) VALUES (1, ?)
ON CONFLICT (id) DO UPDATE SET settings = excluded.settings;
