-- Tracks resolved to MusicBrainz (MAD-713), for cover art from the Cover
-- Art Archive and, later, liner notes. Misses are kept too, with method
-- '', so a song isn't looked up every time it's queued.

-- +goose Up
CREATE TABLE musicbrainz_tracks (
    provider           TEXT NOT NULL,
    track_id           TEXT NOT NULL,
    recording_mbid     TEXT NOT NULL,
    release_mbid       TEXT NOT NULL,
    release_group_mbid TEXT NOT NULL,
    artist_mbid        TEXT NOT NULL,
    -- How it was found: 'mbid' (from the provider), 'isrc' or 'search'. '' means not found.
    method             TEXT NOT NULL CHECK (method IN ('', 'mbid', 'isrc', 'search')),
    resolved_at        TIMESTAMP NOT NULL,
    expires_at         TIMESTAMP NOT NULL,
    PRIMARY KEY (provider, track_id)
);

-- +goose Down
DROP TABLE musicbrainz_tracks;
