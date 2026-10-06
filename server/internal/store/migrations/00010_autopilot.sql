-- Autopilot (MAD-718) adds songs when a room's queue runs dry. Its items
-- carry autopilot, a queue.AutopilotInfo as JSON (what seeded the song).
-- NULL means a member queued it. added_by is still a user: whose taste
-- seeded the song. Autopilot songs aren't in that user's lane, don't take
-- their turn, and aren't credited to them.

-- +goose Up
ALTER TABLE queue_items ADD COLUMN autopilot TEXT;

-- +goose Down
ALTER TABLE queue_items DROP COLUMN autopilot;
