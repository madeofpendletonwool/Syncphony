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
