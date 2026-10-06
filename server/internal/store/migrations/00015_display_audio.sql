-- A paired display can also play the room's audio: it becomes the
-- speaker, as a phone would. Off unless someone in the room turns it on.

-- +goose Up
ALTER TABLE displays ADD COLUMN audio BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE displays DROP COLUMN audio;
