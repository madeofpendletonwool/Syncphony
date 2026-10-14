-- Saved artists (MAD-839): artists someone wants to remember, often one a
-- friend queued. They're personal and belong to no service: an artist is
-- one row however many services it was saved from.

-- +goose Up
CREATE TABLE saved_artists (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The artist's simplified name (match.Simplify): saving them again,
    -- from any service, finds this row.
    key         TEXT NOT NULL,
    name        TEXT NOT NULL,
    -- Where they've been seen, JSON [{"linkId", "artistId"}], newest
    -- first: hints for opening their page. A link may since be gone.
    refs        TEXT NOT NULL DEFAULT '[]',
    -- Their Deezer picture's ID, once the music graph knows it.
    picture     TEXT,
    -- Where they were heard, when saved from a room's song: the room, and
    -- who queued it.
    room_id     TEXT REFERENCES rooms (id) ON DELETE SET NULL,
    queued_by   TEXT REFERENCES users (id) ON DELETE SET NULL,
    saved_at    TIMESTAMP NOT NULL,
    UNIQUE (user_id, key)
);
CREATE INDEX saved_artists_user ON saved_artists (user_id, saved_at);

-- +goose Down
DROP TABLE saved_artists;
