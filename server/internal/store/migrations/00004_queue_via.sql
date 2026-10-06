-- When a song's own service can't play it, the room may play the same
-- recording through another member's service (MAD-704). via_* record where
-- it actually plays from, so the stream endpoint, a restart and the UI all
-- agree. NULL means its own link.

-- +goose Up
ALTER TABLE queue_items ADD COLUMN via_provider TEXT;
ALTER TABLE queue_items ADD COLUMN via_link_id TEXT REFERENCES service_links (id) ON DELETE SET NULL;
ALTER TABLE queue_items ADD COLUMN via_track_id TEXT;

-- +goose Down
ALTER TABLE queue_items DROP COLUMN via_track_id;
ALTER TABLE queue_items DROP COLUMN via_link_id;
ALTER TABLE queue_items DROP COLUMN via_provider;
