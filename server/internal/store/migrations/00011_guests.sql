-- Guests (MAD-720): someone at the hangout scans a room's guest pass and
-- adds songs without an invite or passkey. A guest is a user (so their
-- songs get a lane like anyone's), with a guests row that ties them to one
-- room until they expire. When they do, or a host removes them, their
-- sessions and waiting songs go; the user row stays, with just a display
-- name and color, so the room's history and recaps still show who played
-- what.

-- +goose Up
CREATE TABLE guest_passes (
    id         TEXT PRIMARY KEY,
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    created_by TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    -- Set when revoked, or replaced by a newer pass for the room.
    revoked_at TIMESTAMP
);
CREATE INDEX guest_passes_room ON guest_passes (room_id, created_at);

CREATE TABLE guests (
    user_id    TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    pass_id    TEXT REFERENCES guest_passes (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    -- Set when the guest expired or was removed, and purged.
    ended_at   TIMESTAMP
);
CREATE INDEX guests_room ON guests (room_id);
CREATE INDEX guests_live ON guests (expires_at) WHERE ended_at IS NULL;

-- Keys the server signs things with, such as guest passes, made on first use.
CREATE TABLE server_keys (
    name       TEXT PRIMARY KEY,
    key        BLOB NOT NULL,
    created_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE server_keys;
DROP TABLE guests;
DROP TABLE guest_passes;
