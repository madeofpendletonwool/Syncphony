-- The colors of a queued song's artwork (MAD-714), as palette.Palette JSON,
-- worked out once on the server so every phone in the room matches. NULL
-- until computed.

-- +goose Up
ALTER TABLE queue_items ADD COLUMN palette TEXT;

-- +goose Down
ALTER TABLE queue_items DROP COLUMN palette;
