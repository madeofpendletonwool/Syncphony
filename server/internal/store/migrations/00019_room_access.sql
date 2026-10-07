-- Who can see and join a room. An open room is everyone's on the server,
-- as before. An unlisted room is for its members and anyone with one of
-- its invite links; a private room is for the members its owner lets in.
-- Members are recorded only for rooms that aren't open; the owner is
-- always in, without a row.

-- +goose Up
ALTER TABLE rooms ADD COLUMN visibility TEXT NOT NULL DEFAULT 'open';

CREATE TABLE room_members (
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- 'member', or 'pending': asked to join a private room that approves
    -- who joins, and not let in yet.
    status     TEXT NOT NULL CHECK (status IN ('member', 'pending')),
    added_by   TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (room_id, user_id)
);
CREATE INDEX room_members_user ON room_members (user_id);

-- Links that let a member of the server into a room that isn't open.
CREATE TABLE room_invites (
    code       TEXT PRIMARY KEY,
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    created_by TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    -- NULL: until revoked.
    expires_at TIMESTAMP,
    -- NULL: any number of people.
    max_uses   INTEGER,
    uses       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX room_invites_room ON room_invites (room_id, created_at);

-- +goose Down
DROP TABLE room_invites;
DROP TABLE room_members;
ALTER TABLE rooms DROP COLUMN visibility;
