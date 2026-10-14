-- name: ListSavedArtists :many
SELECT * FROM saved_artists WHERE user_id = ? ORDER BY saved_at DESC;

-- name: GetSavedArtistByKey :one
SELECT * FROM saved_artists WHERE user_id = ? AND key = ?;

-- name: InsertSavedArtist :exec
INSERT INTO saved_artists (id, user_id, key, name, refs, picture, room_id, queued_by, saved_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateSavedArtistRefs :exec
UPDATE saved_artists SET refs = ? WHERE id = ?;

-- name: SetSavedArtistPicture :exec
UPDATE saved_artists SET picture = ? WHERE id = ?;

-- name: DeleteSavedArtist :execrows
DELETE FROM saved_artists WHERE id = ? AND user_id = ?;
