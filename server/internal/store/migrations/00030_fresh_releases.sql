-- Fresh releases (MAD-822, 823, ADR 0017): recent albums, EPs and singles
-- by a set's seeds and their neighbors.

-- +goose Up
-- How far back a set's Fresh feed looks, in days.
ALTER TABLE discovery_sets ADD COLUMN fresh_days INTEGER NOT NULL DEFAULT 60 CHECK (fresh_days IN (30, 60, 90));

-- An artist's recent releases, from ListenBrainz, MusicBrainz and Deezer,
-- merged; JSON. Kept a day.
CREATE TABLE artist_releases (
    -- The artist's simplified name (match.Simplify).
    key         TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    releases    TEXT NOT NULL,
    fetched_at  TIMESTAMP NOT NULL,
    expires_at  TIMESTAMP NOT NULL
);
CREATE INDEX artist_releases_expires ON artist_releases (expires_at);

-- +goose Down
DROP TABLE artist_releases;
ALTER TABLE discovery_sets DROP COLUMN fresh_days;
