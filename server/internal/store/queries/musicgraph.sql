-- name: GetMusicGraphArtist :one
SELECT * FROM musicgraph_artists WHERE key = ? AND expires_at > sqlc.arg(now);

-- name: GetMusicGraphArtistByMBID :one
SELECT * FROM musicgraph_artists WHERE mbid = ? AND mbid != '' AND expires_at > sqlc.arg(now)
ORDER BY fetched_at DESC LIMIT 1;

-- name: PutMusicGraphArtist :exec
INSERT INTO musicgraph_artists (key, mbid, found, facts, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET
    mbid = excluded.mbid, found = excluded.found, facts = excluded.facts,
    fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: DeleteExpiredMusicGraphArtists :exec
DELETE FROM musicgraph_artists WHERE expires_at <= ?;

-- name: GetMusicGraphTrack :one
SELECT * FROM musicgraph_tracks WHERE key = ? AND expires_at > sqlc.arg(now);

-- name: PutMusicGraphTrack :exec
INSERT INTO musicgraph_tracks (key, found, facts, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET
    found = excluded.found, facts = excluded.facts,
    fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: DeleteExpiredMusicGraphTracks :exec
DELETE FROM musicgraph_tracks WHERE expires_at <= ?;

-- name: ListMusicGraphArtists :many
SELECT * FROM musicgraph_artists WHERE expires_at > sqlc.arg(now) ORDER BY fetched_at;

-- name: ListMusicGraphTracks :many
SELECT * FROM musicgraph_tracks WHERE expires_at > sqlc.arg(now) ORDER BY fetched_at;

-- name: GetMusicGraphTag :one
SELECT * FROM musicgraph_tags WHERE key = ? AND expires_at > sqlc.arg(now);

-- name: PutMusicGraphTag :exec
INSERT INTO musicgraph_tags (key, found, facts, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET
    found = excluded.found, facts = excluded.facts,
    fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: DeleteExpiredMusicGraphTags :exec
DELETE FROM musicgraph_tags WHERE expires_at <= ?;

-- TaggedArtists are cached artists carrying a tag, most strongly first:
-- a genre's artists when no source can say.
-- name: TaggedArtists :many
SELECT CAST(json_extract(musicgraph_artists.facts, '$.ref.name') AS TEXT) AS name,
  CAST(json_extract(tag.value, '$.weight') AS REAL) AS weight
FROM musicgraph_artists, json_each(musicgraph_artists.facts, '$.tags') AS tag
WHERE musicgraph_artists.found AND musicgraph_artists.expires_at > sqlc.arg(now)
  AND lower(json_extract(tag.value, '$.name')) = lower(sqlc.arg(tag))
ORDER BY weight DESC
LIMIT sqlc.arg(limit);
