-- Who took a song out of the queue, so only they (or the room's owner)
-- can undo it. NULL for songs not removed, or removed before this.

-- +goose Up
ALTER TABLE queue_items ADD COLUMN removed_by TEXT;

-- +goose Down
ALTER TABLE queue_items DROP COLUMN removed_by;
