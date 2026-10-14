-- Room chat (MAD-842, ADR 0018): messages to everyone in a room, kept
-- with the night they were sent in.

-- +goose Up
CREATE TABLE chat_messages (
    id            TEXT PRIMARY KEY,
    room_id       TEXT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    -- The night the message was sent in, set when that night ends.
    night_id      TEXT REFERENCES nights (id) ON DELETE SET NULL,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Plain text, 1-500 characters; cleared when the message is deleted.
    body          TEXT NOT NULL,
    -- The song playing when it was sent.
    queue_item_id TEXT REFERENCES queue_items (id) ON DELETE SET NULL,
    created_at    TIMESTAMP NOT NULL,
    deleted_at    TIMESTAMP
);
-- IDs are time-ordered, so paging goes by ID.
CREATE INDEX chat_messages_room ON chat_messages (room_id, id);
CREATE INDEX chat_messages_unassigned ON chat_messages (room_id) WHERE night_id IS NULL;

-- +goose Down
DROP TABLE chat_messages;
