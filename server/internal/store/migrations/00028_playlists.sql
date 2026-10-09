-- Syncphony's own playlists (MAD-737, ADR 0016). A playlist lives here,
-- not on a service: its songs keep the snapshot and link they came from,
-- like queue items, so a night saved as a playlist can mix everyone's
-- services. One belongs to whoever made it, and can be shared with a room.

-- +goose Up
CREATE TABLE playlists (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    -- Shared with a room: everyone who can enter it sees and plays it.
    -- NULL keeps it to its owner.
    room_id     TEXT REFERENCES rooms (id) ON DELETE SET NULL,
    -- A night saved as a playlist: the room, and the stretch of it saved.
    -- The recap of that night links to it.
    night_room_id TEXT REFERENCES rooms (id) ON DELETE SET NULL,
    night_from  TIMESTAMP,
    night_to    TIMESTAMP,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);
CREATE INDEX playlists_owner ON playlists (owner_id, updated_at);
CREATE INDEX playlists_room ON playlists (room_id, updated_at) WHERE room_id IS NOT NULL;
CREATE INDEX playlists_night ON playlists (night_room_id, night_from) WHERE night_room_id IS NOT NULL;

CREATE TABLE playlist_songs (
    id          TEXT PRIMARY KEY,
    playlist_id TEXT NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
    -- Order in the playlist, from 0, without gaps.
    position    INTEGER NOT NULL,
    -- provider.TrackRef, as in queue_items. link_id goes NULL if the link
    -- is deleted; the song then can't be queued, but still shows.
    provider    TEXT NOT NULL,
    link_id     TEXT REFERENCES service_links (id) ON DELETE SET NULL,
    track_id    TEXT NOT NULL,
    -- provider.Track when it was added, as JSON.
    metadata    TEXT NOT NULL,
    -- Who brought the song: who queued it, for a saved night, or who
    -- added it to the playlist.
    added_by    TEXT REFERENCES users (id) ON DELETE SET NULL,
    added_at    TIMESTAMP NOT NULL
);
CREATE INDEX playlist_songs_order ON playlist_songs (playlist_id, position);

-- +goose Down
DROP TABLE playlist_songs;
DROP TABLE playlists;
