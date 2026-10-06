-- Paired displays (MAD-716): a TV, projector or spare tablet showing one
-- room's big-screen view. A display can read its room and nothing else;
-- it signs in with its own token, never a user's session.

-- +goose Up
CREATE TABLE displays (
    id           TEXT PRIMARY KEY,
    -- SHA-256 of the display's token. The token itself is never stored.
    token_hash   BLOB NOT NULL UNIQUE,
    room_id      TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    -- Who paired it. NULL once they're gone; the display keeps working.
    paired_by    TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at   TIMESTAMP NOT NULL,
    last_seen_at TIMESTAMP NOT NULL,
    expires_at   TIMESTAMP NOT NULL
);
CREATE INDEX displays_room ON displays (room_id);

-- +goose Down
DROP TABLE displays;
