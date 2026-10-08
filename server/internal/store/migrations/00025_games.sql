-- Music party games (Phase 10, ADR 0015). The round engine runs rounds in
-- memory, like playback, and keeps each finished round and its answers
-- for the night's scores. A night keeps its awards, for recaps.

-- +goose Up
CREATE TABLE game_rounds (
    id            TEXT PRIMARY KEY,
    room_id       TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    -- The song it was about, if it's still around.
    queue_item_id TEXT REFERENCES queue_items (id) ON DELETE SET NULL,
    kind          TEXT NOT NULL,
    -- The quiz.Question, as JSON.
    question      TEXT NOT NULL,
    -- Who started it; NULL when the room's frequency did.
    started_by    TEXT REFERENCES users (id) ON DELETE SET NULL,
    started_at    TIMESTAMP NOT NULL,
    revealed_at   TIMESTAMP NOT NULL
);
CREATE INDEX game_rounds_room ON game_rounds (room_id, started_at);

CREATE TABLE game_answers (
    round_id    TEXT NOT NULL REFERENCES game_rounds (id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The quiz.Response, as JSON.
    answer      TEXT NOT NULL,
    correct     BOOLEAN NOT NULL,
    points      INTEGER NOT NULL,
    answered_at TIMESTAMP NOT NULL,
    PRIMARY KEY (round_id, user_id)
);

-- The night's awards (awards.Award), as a JSON array.
ALTER TABLE nights ADD COLUMN awards TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE nights DROP COLUMN awards;
DROP TABLE game_answers;
DROP TABLE game_rounds;
