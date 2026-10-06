-- name: GetMusicBrainzTrack :one
SELECT * FROM musicbrainz_tracks WHERE provider = ? AND track_id = ? AND expires_at > sqlc.arg(now);

-- name: PutMusicBrainzTrack :exec
INSERT INTO musicbrainz_tracks (provider, track_id, recording_mbid, release_mbid, release_group_mbid, artist_mbid, method, resolved_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (provider, track_id) DO UPDATE SET
    recording_mbid = excluded.recording_mbid, release_mbid = excluded.release_mbid,
    release_group_mbid = excluded.release_group_mbid, artist_mbid = excluded.artist_mbid,
    method = excluded.method, resolved_at = excluded.resolved_at, expires_at = excluded.expires_at;

-- name: DeleteExpiredMusicBrainzTracks :exec
DELETE FROM musicbrainz_tracks WHERE expires_at <= ?;
