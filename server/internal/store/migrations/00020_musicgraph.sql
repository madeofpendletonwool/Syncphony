-- The music knowledge layer (MAD-751, ADR 0012): what Last.fm,
-- ListenBrainz, Deezer and MusicBrainz say about an artist (similar
-- artists, top songs, tags) and a song (similar songs, BPM, popularity),
-- merged. Misses are kept too, with found false, so an unknown artist
-- isn't looked up on every fill.

-- +goose Up
CREATE TABLE musicgraph_artists (
    -- The artist's simplified name (match.Simplify).
    key        TEXT PRIMARY KEY,
    -- MusicBrainz artist ID, '' if unknown.
    mbid       TEXT NOT NULL,
    found      BOOLEAN NOT NULL,
    -- musicgraph.Artist as JSON.
    facts      TEXT NOT NULL,
    fetched_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL
);
CREATE INDEX musicgraph_artists_mbid ON musicgraph_artists (mbid) WHERE mbid != '';

CREATE TABLE musicgraph_tracks (
    -- The simplified artist and title, joined by a NUL.
    key        TEXT PRIMARY KEY,
    found      BOOLEAN NOT NULL,
    -- musicgraph.Track as JSON.
    facts      TEXT NOT NULL,
    fetched_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE musicgraph_tracks;
DROP TABLE musicgraph_artists;
