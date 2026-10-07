-- Admins manage accounts: disable, remove, and an audit log of who did what.

-- +goose Up

-- Set while the account can't sign in. Its sessions are gone; its history stays.
ALTER TABLE users ADD COLUMN disabled_at TIMESTAMP;
-- Set once the account is removed. The row stays, anonymized, so the
-- history of the rooms they were in still adds up.
ALTER TABLE users ADD COLUMN removed_at TIMESTAMP;

CREATE TABLE user_audit (
    id          TEXT PRIMARY KEY,
    -- Who did it, and their name then. NULL actor_id means the CLI.
    actor_id    TEXT REFERENCES users (id) ON DELETE SET NULL,
    actor_name  TEXT NOT NULL,
    -- Whose account, and their name then: a removed account's name is
    -- gone from users, but the log keeps it.
    target_id   TEXT REFERENCES users (id) ON DELETE SET NULL,
    target_name TEXT NOT NULL,
    action      TEXT NOT NULL CHECK (action IN ('role_changed', 'disabled', 'enabled', 'removed', 'deleted_self')),
    -- For role_changed, the new role.
    detail      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL
);
CREATE INDEX user_audit_created ON user_audit (created_at);

-- +goose Down

DROP TABLE user_audit;
ALTER TABLE users DROP COLUMN removed_at;
ALTER TABLE users DROP COLUMN disabled_at;
