-- Syncphony's own playlists (MAD-737).

-- name: CreatePlaylist :one
INSERT INTO playlists (id, owner_id, name, room_id, night_room_id, night_from, night_to, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: GetPlaylist :one
SELECT * FROM playlists WHERE id = ?;

-- ListPlaylistsFor returns a user's own playlists and every playlist
-- shared with a room, most recently changed first. Whether the user can
-- enter those rooms is checked in Go.
-- name: ListPlaylistsFor :many
SELECT playlists.*,
    CAST((SELECT count(*) FROM playlist_songs WHERE playlist_songs.playlist_id = playlists.id) AS INTEGER) AS songs
FROM playlists
WHERE owner_id = sqlc.arg(user_id) OR room_id IS NOT NULL
ORDER BY updated_at DESC, id DESC;

-- NightPlaylists returns the playlists saved from a room's night that
-- began in [from, to).
-- name: NightPlaylists :many
SELECT * FROM playlists
WHERE night_room_id = sqlc.arg(room_id) AND night_from >= sqlc.arg(from_at) AND night_from < sqlc.arg(to_at)
ORDER BY created_at, id;

-- name: UpdatePlaylist :exec
UPDATE playlists SET name = ?, room_id = ?, updated_at = ? WHERE id = ?;

-- name: TouchPlaylist :exec
UPDATE playlists SET updated_at = ? WHERE id = ?;

-- name: DeletePlaylist :exec
DELETE FROM playlists WHERE id = ?;

-- name: AddPlaylistSong :exec
INSERT INTO playlist_songs (id, playlist_id, position, provider, link_id, track_id, metadata, added_by, added_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CountPlaylistSongs :one
SELECT count(*) FROM playlist_songs WHERE playlist_id = ?;

-- name: ListPlaylistSongs :many
SELECT * FROM playlist_songs WHERE playlist_id = ? ORDER BY position, id;

-- PlaylistCovers returns the first few songs of each playlist given, for
-- their covers.
-- name: PlaylistCovers :many
SELECT * FROM playlist_songs
WHERE playlist_id IN (sqlc.slice(ids)) AND position < sqlc.arg(n)
ORDER BY playlist_id, position;

-- name: GetPlaylistSong :one
SELECT * FROM playlist_songs WHERE id = ?;

-- name: DeletePlaylistSong :exec
DELETE FROM playlist_songs WHERE id = ?;

-- name: SetPlaylistSongPosition :exec
UPDATE playlist_songs SET position = ? WHERE id = ?;
