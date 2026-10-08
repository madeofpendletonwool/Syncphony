-- name: GetCachedLinerNotes :one
SELECT * FROM liner_notes_cache WHERE provider = ? AND track_id = ? AND expires_at > sqlc.arg(now);

-- name: PutCachedLinerNotes :exec
INSERT INTO liner_notes_cache (provider, track_id, found, notes, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, track_id) DO UPDATE SET
    found = excluded.found, notes = excluded.notes, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: DeleteExpiredLinerNotes :exec
DELETE FROM liner_notes_cache WHERE expires_at <= ?;

-- name: GetCachedPageNotes :one
SELECT * FROM page_notes_cache WHERE kind = ? AND key = ? AND expires_at > sqlc.arg(now);

-- name: PutCachedPageNotes :exec
INSERT INTO page_notes_cache (kind, key, found, notes, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (kind, key) DO UPDATE SET
    found = excluded.found, notes = excluded.notes, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: DeleteExpiredPageNotes :exec
DELETE FROM page_notes_cache WHERE expires_at <= ?;
