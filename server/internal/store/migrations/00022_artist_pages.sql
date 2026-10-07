-- Artist and album pages (MAD-763). Their notes: an artist's bio, facts,
-- members, genres and releases, an album's type, labels and notes, from
-- MusicBrainz and Wikipedia. And a genre's top artists, from the music
-- knowledge layer. Misses are kept too, with found false.

-- +goose Up
CREATE TABLE page_notes_cache (
    kind       TEXT NOT NULL CHECK (kind IN ('artist', 'album')),
    -- The artist's simplified name; for an album, its artist's and its
    -- title's, joined by a NUL.
    key        TEXT NOT NULL,
    found      BOOLEAN NOT NULL,
    -- linernotes.ArtistNotes or linernotes.AlbumNotes as JSON; '{}' when
    -- not found.
    notes      TEXT NOT NULL,
    fetched_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    PRIMARY KEY (kind, key)
);

CREATE TABLE musicgraph_tags (
    -- The tag, simplified (match.Simplify).
    key        TEXT PRIMARY KEY,
    found      BOOLEAN NOT NULL,
    -- musicgraph.Genre as JSON.
    facts      TEXT NOT NULL,
    fetched_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE musicgraph_tags;
DROP TABLE page_notes_cache;
