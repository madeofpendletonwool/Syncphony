-- Song of the night (MAD-721). Anyone in the room can heart the song
-- that's playing. When the night ends (the host ends it, or the room goes
-- quiet), its most-hearted song is crowned and kept with the night, for
-- recaps.

-- +goose Up
CREATE TABLE hearts (
    queue_item_id TEXT NOT NULL REFERENCES queue_items (id) ON DELETE CASCADE,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at    TIMESTAMP NOT NULL,
    PRIMARY KEY (queue_item_id, user_id)
);

CREATE TABLE nights (
    id            TEXT PRIMARY KEY,
    room_id       TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    -- When the night's first song started, and when it ended.
    started_at    TIMESTAMP NOT NULL,
    ended_at      TIMESTAMP NOT NULL,
    ended_by      TEXT NOT NULL CHECK (ended_by IN ('host', 'idle')),
    plays         INTEGER NOT NULL,
    -- The song of the night, and its hearts. NULL if nothing got a heart.
    queue_item_id TEXT REFERENCES queue_items (id) ON DELETE SET NULL,
    hearts        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX nights_room ON nights (room_id, ended_at);

-- +goose Down
DROP TABLE nights;
DROP TABLE hearts;
