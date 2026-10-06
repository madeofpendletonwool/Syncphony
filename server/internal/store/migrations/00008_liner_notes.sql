-- Liner notes for queued tracks (MAD-717): credits and facts from
-- MusicBrainz, and the artist's bio from Wikipedia. Misses are kept too,
-- with found false, so a song isn't looked up every time it plays.

-- +goose Up
CREATE TABLE liner_notes_cache (
    provider   TEXT NOT NULL,
    track_id   TEXT NOT NULL,
    found      BOOLEAN NOT NULL,
    -- linernotes.Notes as JSON; '{}' when not found.
    notes      TEXT NOT NULL,
    fetched_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    PRIMARY KEY (provider, track_id)
);

-- +goose Down
DROP TABLE liner_notes_cache;
