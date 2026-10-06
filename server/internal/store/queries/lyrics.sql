-- name: GetCachedLyrics :one
SELECT * FROM lyrics_cache WHERE provider = ? AND track_id = ? AND expires_at > sqlc.arg(now);

-- name: PutCachedLyrics :exec
INSERT INTO lyrics_cache (provider, track_id, source, instrumental, plain, synced, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, track_id) DO UPDATE SET
    source = excluded.source, instrumental = excluded.instrumental, plain = excluded.plain,
    synced = excluded.synced, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: DeleteExpiredLyrics :exec
DELETE FROM lyrics_cache WHERE expires_at <= ?;
