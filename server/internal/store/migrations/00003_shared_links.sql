-- A link's owner can share it: everyone on the server can then search,
-- browse and queue from it. Only providers that allow it (self-hosted
-- libraries, not personal subscriptions) can be shared.

-- +goose Up
ALTER TABLE service_links ADD COLUMN shared BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE service_links DROP COLUMN shared;
