-- When a song was put back at the front of the queue because someone went
-- back to the song before it. Such songs play before the fair order, the
-- latest first. NULL for every other song.

-- +goose Up
ALTER TABLE queue_items ADD COLUMN resume_at TIMESTAMP;

-- +goose Down
ALTER TABLE queue_items DROP COLUMN resume_at;
