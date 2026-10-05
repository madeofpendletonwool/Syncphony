-- Rooms get a queue version, bumped on every queue change. Realtime clients
-- resume with the last version they saw; it survives restarts, so a stale
-- client is never mistaken for an up-to-date one.

-- +goose Up
ALTER TABLE rooms ADD COLUMN queue_version INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE rooms DROP COLUMN queue_version;
