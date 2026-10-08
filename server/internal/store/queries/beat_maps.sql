-- name: GetBeatMap :one
SELECT * FROM beat_maps WHERE provider = ? AND track_id = ?;

-- name: PutBeatMap :exec
INSERT INTO beat_maps (provider, track_id, version, found, map, analyzed_at, used_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, track_id) DO UPDATE SET
    version = excluded.version, found = excluded.found, map = excluded.map,
    analyzed_at = excluded.analyzed_at, used_at = excluded.used_at;

-- TouchBeatMap marks a map as used, at most once a day.
-- name: TouchBeatMap :exec
UPDATE beat_maps SET used_at = sqlc.arg(now)
WHERE provider = sqlc.arg(provider) AND track_id = sqlc.arg(track_id) AND used_at < sqlc.arg(since);

-- SweepBeatMaps forgets maps nobody has played in a long while, and
-- misses old enough to try again.
-- name: SweepBeatMaps :execrows
DELETE FROM beat_maps
WHERE used_at < sqlc.arg(unused_since) OR (NOT found AND analyzed_at < sqlc.arg(missed_before));
