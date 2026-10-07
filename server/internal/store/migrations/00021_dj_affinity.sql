-- The DJ's long-term memory of each room's taste (MAD-756, ADR 0012):
-- what it liked and skipped over past nights. Each finished night is
-- folded in once, and every night before it then counts 0.7 times as
-- much, so last weekend still counts and three months ago is faint. It's
-- only a weak prior: tonight's taste is read from the room's plays.

-- +goose Up
CREATE TABLE dj_rooms (
    room_id        TEXT PRIMARY KEY REFERENCES rooms (id) ON DELETE CASCADE,
    -- How many nights have been folded in.
    nights         INTEGER NOT NULL,
    -- When the last night folded in ended.
    folded_through TIMESTAMP NOT NULL,
    updated_at     TIMESTAMP NOT NULL
);

CREATE TABLE dj_affinities (
    room_id    TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('artist', 'tag')),
    -- The artist's simplified name (match.Simplify), or the tag.
    key        TEXT NOT NULL,
    -- Whose taste it is: a user ID, or '' for autopilot's songs the room
    -- let play, and for skips, which are the room's.
    member     TEXT NOT NULL,
    name       TEXT NOT NULL,
    -- Likes and skips, as the DJ's signals add up, faded by night.
    likes      REAL NOT NULL,
    skips      REAL NOT NULL,
    -- The night (counting from 1) the room last liked it, and when that
    -- night ended; 0 and NULL if it never did.
    last_night INTEGER NOT NULL,
    last_at    TIMESTAMP,
    PRIMARY KEY (room_id, kind, key, member)
);

-- +goose Down
DROP TABLE dj_affinities;
DROP TABLE dj_rooms;
