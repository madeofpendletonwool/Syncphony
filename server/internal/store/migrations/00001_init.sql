-- Initial schema. Conventions (see internal/store/doc.go):
--   * IDs are UUIDv7 strings generated in Go.
--   * Times are TIMESTAMP columns written from Go, in UTC.
--   * Enums are TEXT with CHECK constraints.
--   * JSON is TEXT; Postgres would use JSONB.

-- +goose Up

CREATE TABLE users (
    id           TEXT PRIMARY KEY,
    -- Lowercase login name, for password sign-in and @mentions.
    username     TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    -- Asset key or URL; NULL shows initials on color.
    avatar       TEXT,
    -- Lane color in the queue UI, e.g. "#7c3aed".
    color        TEXT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    created_at   TIMESTAMP NOT NULL
);

CREATE TABLE invites (
    code       TEXT PRIMARY KEY,
    -- NULL for the bootstrap invite the server creates when there are no users.
    created_by TEXT REFERENCES users (id) ON DELETE SET NULL,
    role       TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    created_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    used_by    TEXT UNIQUE REFERENCES users (id) ON DELETE SET NULL,
    used_at    TIMESTAMP
);

CREATE TABLE credentials_password (
    user_id    TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- PHC string, e.g. "$argon2id$v=19$m=...".
    hash       TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE credentials_passkey (
    -- WebAuthn credential ID.
    id           BLOB PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- User-chosen label, e.g. "Pixel 9".
    name         TEXT NOT NULL,
    -- The WebAuthn library's credential record (public key, sign count,
    -- flags, transports) as JSON, so library upgrades don't need migrations.
    data         TEXT NOT NULL,
    created_at   TIMESTAMP NOT NULL,
    last_used_at TIMESTAMP
);
CREATE INDEX credentials_passkey_user ON credentials_passkey (user_id);

CREATE TABLE sessions (
    -- SHA-256 of the session token. The token itself is never stored.
    token_hash   BLOB PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMP NOT NULL,
    last_seen_at TIMESTAMP NOT NULL,
    expires_at   TIMESTAMP NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE service_links (
    id                    TEXT PRIMARY KEY,
    user_id               TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- provider.Info.ID, e.g. "navidrome".
    provider              TEXT NOT NULL,
    -- provider.AccountInfo: stable account ID (for dedupe) and display label.
    account_id            TEXT NOT NULL,
    account_label         TEXT NOT NULL,
    -- Sealed by the credential vault. Opaque here.
    encrypted_credentials BLOB NOT NULL,
    status                TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'expired', 'error')),
    -- Last error shown to the user when status isn't ok.
    status_detail         TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMP NOT NULL,
    updated_at            TIMESTAMP NOT NULL,
    last_ok_at            TIMESTAMP,
    UNIQUE (user_id, provider, account_id)
);

CREATE TABLE rooms (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    owner_id         TEXT NOT NULL REFERENCES users (id),
    -- The device currently acting as the player, if any.
    player_device_id TEXT,
    fairness_mode    TEXT NOT NULL DEFAULT 'round_robin' CHECK (fairness_mode IN ('round_robin', 'fifo')),
    settings         TEXT NOT NULL DEFAULT '{}',
    created_at       TIMESTAMP NOT NULL
);

CREATE TABLE queue_items (
    id            TEXT PRIMARY KEY,
    room_id       TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    added_by      TEXT NOT NULL REFERENCES users (id),
    -- provider.TrackRef. link_id goes NULL if the link is deleted; the
    -- item then can't play, but the snapshot still renders.
    provider      TEXT NOT NULL,
    link_id       TEXT REFERENCES service_links (id) ON DELETE SET NULL,
    track_id      TEXT NOT NULL,
    -- provider.Track at the time it was queued, as JSON.
    metadata      TEXT NOT NULL,
    state         TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'playing', 'played', 'skipped', 'removed')),
    -- Order within the adder's lane. Gaps are fine; fairness interleaves lanes.
    lane_position INTEGER NOT NULL,
    added_at      TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL
);
CREATE INDEX queue_items_room_state ON queue_items (room_id, state);
CREATE INDEX queue_items_lane ON queue_items (room_id, added_by, lane_position);
-- At most one item plays per room.
CREATE UNIQUE INDEX queue_items_one_playing ON queue_items (room_id) WHERE state = 'playing';

CREATE TABLE play_history (
    id            TEXT PRIMARY KEY,
    room_id       TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    queue_item_id TEXT NOT NULL REFERENCES queue_items (id) ON DELETE CASCADE,
    started_at    TIMESTAMP NOT NULL,
    ended_at      TIMESTAMP,
    end_reason    TEXT CHECK (end_reason IN ('finished', 'skipped', 'removed', 'error'))
);
CREATE INDEX play_history_room ON play_history (room_id, started_at);

-- +goose Down

DROP TABLE play_history;
DROP TABLE queue_items;
DROP TABLE rooms;
DROP TABLE service_links;
DROP TABLE sessions;
DROP TABLE credentials_passkey;
DROP TABLE credentials_password;
DROP TABLE invites;
DROP TABLE users;
