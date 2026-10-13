-- Artists' recent releases (MAD-822).

-- name: GetArtistReleases :one
SELECT * FROM artist_releases WHERE key = ? AND expires_at > sqlc.arg(now);

-- name: PutArtistReleases :exec
INSERT INTO artist_releases (key, name, releases, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET
    name = excluded.name, releases = excluded.releases,
    fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: DeleteExpiredArtistReleases :exec
DELETE FROM artist_releases WHERE expires_at <= ?;
