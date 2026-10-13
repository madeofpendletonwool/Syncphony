-- Personal discovery (MAD-815, ADR 0017). Queries that take a user_id
-- only see that user's rows.

-- name: CreateDiscoverySet :one
INSERT INTO discovery_sets (id, user_id, name, explore, mode, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- name: GetDiscoverySet :one
SELECT * FROM discovery_sets WHERE id = ? AND user_id = ?;

-- name: ListDiscoverySets :many
SELECT discovery_sets.*,
    CAST((SELECT count(*) FROM discovery_set_artists a WHERE a.set_id = discovery_sets.id) AS INTEGER) AS artists
FROM discovery_sets
WHERE user_id = ?
ORDER BY updated_at DESC, id DESC;

-- name: UpdateDiscoverySet :exec
UPDATE discovery_sets SET name = ?, explore = ?, mode = ?, fresh_days = ?, updated_at = ? WHERE id = ? AND user_id = ?;

-- name: TouchDiscoverySet :exec
UPDATE discovery_sets SET updated_at = ? WHERE id = ?;

-- name: DeleteDiscoverySet :execrows
DELETE FROM discovery_sets WHERE id = ? AND user_id = ?;

-- name: AddDiscoverySetArtist :exec
INSERT INTO discovery_set_artists (set_id, artist_key, name, mbid, added_from, added_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (set_id, artist_key) DO NOTHING;

-- name: ListDiscoverySetArtists :many
SELECT * FROM discovery_set_artists WHERE set_id = ? ORDER BY added_at, artist_key;

-- ListAllDiscoverySetArtists lists every user's seed artists, once each,
-- to warm their recent releases.
-- name: ListAllDiscoverySetArtists :many
SELECT name, CAST(max(coalesce(mbid, '')) AS TEXT) AS mbid
FROM discovery_set_artists
GROUP BY lower(name)
ORDER BY lower(name);

-- name: RemoveDiscoverySetArtist :execrows
DELETE FROM discovery_set_artists WHERE set_id = ? AND artist_key = ?;

-- PutDiscoveryFeedback records a thumb on a song, replacing the user's
-- earlier one on it.
-- name: PutDiscoveryFeedback :exec
INSERT INTO discovery_feedback (id, user_id, set_id, kind, song_key, title, artist, mbid, isrc, deezer_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, song_key) DO UPDATE SET
    set_id = excluded.set_id, kind = excluded.kind, title = excluded.title, artist = excluded.artist,
    mbid = excluded.mbid, isrc = excluded.isrc, deezer_id = excluded.deezer_id, created_at = excluded.created_at;

-- name: ListDiscoveryFeedback :many
SELECT * FROM discovery_feedback WHERE user_id = ? ORDER BY created_at DESC, id;

-- name: DeleteDiscoveryFeedback :exec
DELETE FROM discovery_feedback WHERE user_id = ? AND song_key = ?;

-- name: BlockDiscoveryArtist :exec
INSERT INTO discovery_blocks (user_id, artist_key, name, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, artist_key) DO NOTHING;

-- name: UnblockDiscoveryArtist :execrows
DELETE FROM discovery_blocks WHERE user_id = ? AND artist_key = ?;

-- name: ListDiscoveryBlocks :many
SELECT * FROM discovery_blocks WHERE user_id = ? ORDER BY name, artist_key;

-- name: AddWantItem :exec
INSERT INTO want_items (id, user_id, set_id, kind, item_key, artist, title, release_title, mbid, isrc, deezer_id, added_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, kind, item_key) DO NOTHING;

-- name: ListWantItems :many
SELECT * FROM want_items WHERE user_id = ? ORDER BY added_at DESC, id;

-- name: SetWantItemStatus :execrows
UPDATE want_items SET status = ?, got_at = ? WHERE id = ? AND user_id = ?;

-- name: PutLibraryIndex :exec
INSERT INTO library_indexes (link_id, scanned_at, built_at, songs, data)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (link_id) DO UPDATE SET
    scanned_at = excluded.scanned_at, built_at = excluded.built_at, songs = excluded.songs, data = excluded.data;

-- name: GetLibraryIndex :one
SELECT * FROM library_indexes WHERE link_id = ?;

-- name: TouchLibraryIndex :exec
UPDATE library_indexes SET built_at = ? WHERE link_id = ?;

-- name: GetDeezerSong :one
SELECT * FROM deezer_songs WHERE song_key = ?;

-- name: PutDeezerSong :exec
INSERT INTO deezer_songs (song_key, deezer_id, album, cover, checked_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (song_key) DO UPDATE SET
    deezer_id = excluded.deezer_id, album = excluded.album, cover = excluded.cover, checked_at = excluded.checked_at;
