-- Account recovery (MAD-725). An admin makes a one-time link for someone
-- who can't sign in; using it sets a new password or passkey and signs out
-- everywhere else. Only the code's hash is stored, and a link is deleted
-- once used.

-- +goose Up
CREATE TABLE reset_links (
    id         TEXT PRIMARY KEY,
    -- SHA-256 of the code in the link. The code itself is never stored.
    code_hash  BLOB NOT NULL UNIQUE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The admin who made it; NULL for links made with the CLI.
    created_by TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL
);
CREATE INDEX reset_links_user ON reset_links (user_id);

-- +goose Down
DROP TABLE reset_links;
