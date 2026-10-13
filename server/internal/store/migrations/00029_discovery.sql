-- Personal discovery (MAD-815, ADR 0017). Taste comes from seed sets a
-- user builds by hand, not from their library: the library only says what
-- they already have (library_indexes). Every row here is one user's own.

-- +goose Up
-- A named list of seed artists, each its own discovery feed.
CREATE TABLE discovery_sets (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    -- How far the feed strays from the seeds, 0 to 100.
    explore     INTEGER NOT NULL DEFAULT 40 CHECK (explore BETWEEN 0 AND 100),
    -- Which feeds: deep cuts (anything new to you), fresh (recent
    -- releases, stage 3), or both.
    mode        TEXT NOT NULL DEFAULT 'both' CHECK (mode IN ('deep', 'fresh', 'both')),
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
CREATE INDEX discovery_sets_user ON discovery_sets (user_id, updated_at);

CREATE TABLE discovery_set_artists (
    set_id      TEXT NOT NULL REFERENCES discovery_sets (id) ON DELETE CASCADE,
    -- The artist's MBID, else their simplified name (match.Simplify).
    artist_key  TEXT NOT NULL,
    name        TEXT NOT NULL,
    mbid        TEXT,
    added_from  TEXT NOT NULL DEFAULT 'manual' CHECK (added_from IN ('manual', 'thumb', 'starred', 'room')),
    added_at    TIMESTAMP NOT NULL,
    PRIMARY KEY (set_id, artist_key)
);

-- Thumbs on songs in a feed. A thumbs-down hides the song from every set
-- of its user's. One per user and song: the latest thumb wins.
CREATE TABLE discovery_feedback (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    set_id      TEXT REFERENCES discovery_sets (id) ON DELETE SET NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('up', 'down')),
    -- The simplified artist and title, which songs are compared by.
    song_key    TEXT NOT NULL,
    title       TEXT NOT NULL,
    artist      TEXT NOT NULL,
    mbid        TEXT,
    isrc        TEXT,
    deezer_id   INTEGER,
    created_at  TIMESTAMP NOT NULL,
    UNIQUE (user_id, song_key)
);

-- The global "not for me": artists kept out of every set's feed.
CREATE TABLE discovery_blocks (
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    artist_key  TEXT NOT NULL,
    name        TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, artist_key)
);

-- Songs and releases a user wants, until they turn up in their library
-- (stage 4 fills this; the feed already leaves them out).
CREATE TABLE want_items (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    set_id        TEXT REFERENCES discovery_sets (id) ON DELETE SET NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('track', 'release')),
    -- The simplified artist and title (or release title).
    item_key      TEXT NOT NULL,
    artist        TEXT NOT NULL,
    title         TEXT NOT NULL,
    release_title TEXT NOT NULL DEFAULT '',
    mbid          TEXT,
    isrc          TEXT,
    deezer_id     INTEGER,
    status        TEXT NOT NULL DEFAULT 'wanted' CHECK (status IN ('wanted', 'got', 'dismissed')),
    got_at        TIMESTAMP,
    added_at      TIMESTAMP NOT NULL,
    UNIQUE (user_id, kind, item_key)
);
CREATE INDEX want_items_user ON want_items (user_id, status, added_at);

-- What's in a link's library, so discovery can leave it out (MAD-816):
-- the keys of its songs, releases and artists, gzipped JSON, loaded into
-- memory at startup and rebuilt daily.
CREATE TABLE library_indexes (
    link_id     TEXT PRIMARY KEY REFERENCES service_links (id) ON DELETE CASCADE,
    -- When the service last scanned its library, if it says; a rebuild is
    -- skipped while that hasn't changed.
    scanned_at  TIMESTAMP,
    built_at    TIMESTAMP NOT NULL,
    songs       INTEGER NOT NULL,
    data        BLOB NOT NULL
);

-- Songs' Deezer track IDs, found by searching once, for previews. 0 is
-- no match.
CREATE TABLE deezer_songs (
    song_key    TEXT PRIMARY KEY,
    deezer_id   INTEGER NOT NULL,
    album       TEXT NOT NULL DEFAULT '',
    cover       TEXT NOT NULL DEFAULT '',
    checked_at  TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE deezer_songs;
DROP TABLE library_indexes;
DROP TABLE want_items;
DROP TABLE discovery_blocks;
DROP TABLE discovery_feedback;
DROP TABLE discovery_set_artists;
DROP TABLE discovery_sets;
