-- Personal discovery, stage 4 (MAD-824..826, ADR 0017): the Want list
-- with automatic got-it, sets that learn from thumbs, and a room's taste
-- as a set's source.

-- +goose Up
-- A Want-list item's cover (Deezer's image ID), how it came to be got
-- ('auto' when it turned up in the library, 'manual' when you said so),
-- and whether you've seen that it landed.
ALTER TABLE want_items ADD COLUMN cover TEXT NOT NULL DEFAULT '';
ALTER TABLE want_items ADD COLUMN got_by TEXT CHECK (got_by IN ('auto', 'manual'));
ALTER TABLE want_items ADD COLUMN got_seen INTEGER NOT NULL DEFAULT 0;
CREATE INDEX want_items_set ON want_items (set_id, status);

-- Whether artists you thumb up twice from a set's feed join it by
-- themselves, without asking.
ALTER TABLE discovery_sets ADD COLUMN grow_auto INTEGER NOT NULL DEFAULT 0;
-- A room whose taste the set mixes in (live), and how much: 'low',
-- 'medium' or 'high'. Read only: discovery never writes to the room.
ALTER TABLE discovery_sets ADD COLUMN room_id TEXT REFERENCES rooms (id) ON DELETE SET NULL;
ALTER TABLE discovery_sets ADD COLUMN room_weight TEXT NOT NULL DEFAULT 'medium' CHECK (room_weight IN ('low', 'medium', 'high'));

-- Suggestions you said no to: an artist to add to a set ('grow', scope is
-- the set) or to block ('block', scope is ''). They aren't offered again.
CREATE TABLE discovery_dismissed (
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('grow', 'block')),
    scope       TEXT NOT NULL DEFAULT '',
    artist_key  TEXT NOT NULL,
    created_at  TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, kind, scope, artist_key)
);

-- +goose Down
DROP TABLE discovery_dismissed;
ALTER TABLE discovery_sets DROP COLUMN room_weight;
ALTER TABLE discovery_sets DROP COLUMN room_id;
ALTER TABLE discovery_sets DROP COLUMN grow_auto;
DROP INDEX want_items_set;
ALTER TABLE want_items DROP COLUMN got_seen;
ALTER TABLE want_items DROP COLUMN got_by;
ALTER TABLE want_items DROP COLUMN cover;
