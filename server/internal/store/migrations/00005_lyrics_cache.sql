-- Lyrics found for tracks, by the track's own provider or from LRCLIB
-- (MAD-712). Misses are cached too, with source '', so a room full of
-- phones doesn't ask LRCLIB about the same song over and over.

-- +goose Up
CREATE TABLE lyrics_cache (
    provider     TEXT NOT NULL,
    track_id     TEXT NOT NULL,
    -- Where they came from: a provider ID or 'lrclib'. '' means none were found.
    source       TEXT NOT NULL,
    instrumental BOOLEAN NOT NULL,
    plain        TEXT NOT NULL,
    -- Synced lines as JSON, [{"ms": 1234, "text": "..."}]; '[]' when unsynced.
    synced       TEXT NOT NULL,
    fetched_at   TIMESTAMP NOT NULL,
    expires_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (provider, track_id)
);

-- +goose Down
DROP TABLE lyrics_cache;
