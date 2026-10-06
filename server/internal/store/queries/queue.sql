-- name: AddQueueItem :one
INSERT INTO queue_items (id, room_id, added_by, provider, link_id, track_id, metadata, lane_position, added_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, sqlc.arg(now), sqlc.arg(now))
RETURNING *;

-- NextLanePosition is where a new item goes at the end of a user's lane.
-- name: NextLanePosition :one
SELECT CAST(coalesce(max(lane_position), 0) + 1024 AS INTEGER)
FROM queue_items
WHERE room_id = ? AND added_by = ? AND state = 'queued';

-- name: GetQueueItem :one
SELECT * FROM queue_items WHERE id = ?;

-- ListUpcoming returns the playing item and queued items, each lane in
-- order. The fairness engine interleaves the lanes.
-- name: ListUpcoming :many
SELECT * FROM queue_items
WHERE room_id = ? AND state IN ('queued', 'playing')
ORDER BY added_by, lane_position, added_at;

-- name: GetPlaying :one
SELECT * FROM queue_items WHERE room_id = ? AND state = 'playing';

-- name: SetQueueItemState :exec
UPDATE queue_items SET state = ?, updated_at = ? WHERE id = ?;

-- name: MoveQueueItem :exec
UPDATE queue_items SET lane_position = ?, updated_at = ? WHERE id = ? AND state = 'queued';

-- name: StartPlay :one
INSERT INTO play_history (id, room_id, queue_item_id, started_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: EndPlay :exec
UPDATE play_history SET ended_at = ?, end_reason = ? WHERE id = ? AND ended_at IS NULL;

-- name: ListHistory :many
SELECT sqlc.embed(play_history), sqlc.embed(queue_items)
FROM play_history
JOIN queue_items ON queue_items.id = play_history.queue_item_id
WHERE play_history.room_id = ?
ORDER BY play_history.started_at DESC
LIMIT ?;

-- ListLane returns one user's queued items in a room, in lane order.
-- name: ListLane :many
SELECT * FROM queue_items
WHERE room_id = ? AND added_by = ? AND state = 'queued'
ORDER BY lane_position, added_at;

-- LastPlayedByUser is when each user's most recent song started in a room,
-- for the fairness engine. It selects the column itself, not max(), so the
-- driver still knows it's a timestamp.
-- name: LastPlayedByUser :many
SELECT queue_items.added_by AS user_id, play_history.started_at
FROM play_history
JOIN queue_items ON queue_items.id = play_history.queue_item_id
WHERE play_history.room_id = sqlc.arg(room_id)
  AND NOT EXISTS (
    SELECT 1 FROM play_history AS later
    JOIN queue_items AS later_item ON later_item.id = later.queue_item_id
    WHERE later.room_id = play_history.room_id
      AND later_item.added_by = queue_items.added_by
      AND later.started_at > play_history.started_at
  );

-- EndOpenPlays closes a room's unfinished play_history rows. There is at
-- most one: the playing item's.
-- name: EndOpenPlays :exec
UPDATE play_history SET ended_at = ?, end_reason = ? WHERE room_id = ? AND ended_at IS NULL;

-- RecentPlayers is who queued the songs that most recently started in a
-- room, newest first, for fairness rules that look back a few songs.
-- name: RecentPlayers :many
SELECT queue_items.added_by
FROM play_history
JOIN queue_items ON queue_items.id = play_history.queue_item_id
WHERE play_history.room_id = ?
ORDER BY play_history.started_at DESC, play_history.id DESC
LIMIT ?;

-- RecentDuplicates counts a room's items for the same song (the same
-- track, or the same ISRC on any service) that are waiting, playing, or
-- started playing since a time, for the repeat guard. metadata is a
-- provider.Track, whose ISRC field has no JSON tag.
-- name: RecentDuplicates :one
SELECT count(*) FROM queue_items
WHERE queue_items.room_id = sqlc.arg(room_id)
  AND (
    (queue_items.provider = sqlc.arg(provider) AND queue_items.track_id = sqlc.arg(track_id))
    OR (CAST(sqlc.arg(isrc) AS TEXT) != '' AND json_extract(queue_items.metadata, '$.ISRC') = sqlc.arg(isrc))
  )
  AND (
    queue_items.state IN ('queued', 'playing')
    OR EXISTS (
      SELECT 1 FROM play_history
      WHERE play_history.queue_item_id = queue_items.id AND play_history.started_at >= sqlc.arg(since)
    )
  );

-- ListPlayed returns a room's finished plays (not the one in progress)
-- that started before a time, newest first, optionally only one user's
-- songs. Pages go back by passing the last row's started_at.
-- name: ListPlayed :many
SELECT sqlc.embed(play_history), sqlc.embed(queue_items)
FROM play_history
JOIN queue_items ON queue_items.id = play_history.queue_item_id
WHERE play_history.room_id = sqlc.arg(room_id)
  AND play_history.ended_at IS NOT NULL
  AND play_history.started_at < sqlc.arg(before)
  AND (CAST(sqlc.arg(user_id) AS TEXT) = '' OR queue_items.added_by = sqlc.arg(user_id))
ORDER BY play_history.started_at DESC, play_history.id DESC
LIMIT sqlc.arg(limit);

-- ListPlaysBetween returns a room's finished plays that started in
-- [from, to), oldest first, for stats.
-- name: ListPlaysBetween :many
SELECT sqlc.embed(play_history), sqlc.embed(queue_items)
FROM play_history
JOIN queue_items ON queue_items.id = play_history.queue_item_id
WHERE play_history.room_id = sqlc.arg(room_id)
  AND play_history.ended_at IS NOT NULL
  AND play_history.started_at >= sqlc.arg(from_time) AND play_history.started_at < sqlc.arg(to_time)
ORDER BY play_history.started_at, play_history.id
LIMIT sqlc.arg(limit);

-- ListPlayTimes returns when each of a room's finished plays started and
-- ended, and whose song it was, oldest first, to find sessions.
-- name: ListPlayTimes :many
SELECT play_history.started_at, play_history.ended_at, queue_items.added_by
FROM play_history
JOIN queue_items ON queue_items.id = play_history.queue_item_id
WHERE play_history.room_id = ? AND play_history.ended_at IS NOT NULL
ORDER BY play_history.started_at, play_history.id
LIMIT ?;

-- SetQueueItemVia records where an item plays from instead of its own link.
-- name: SetQueueItemVia :exec
UPDATE queue_items SET via_provider = ?, via_link_id = ?, via_track_id = ?, updated_at = ? WHERE id = ?;

-- SetTrackPalette saves a song's palette on every waiting or playing item
-- that's that song, and on the given item.
-- name: SetTrackPalette :exec
UPDATE queue_items SET palette = sqlc.arg(palette)
WHERE provider = sqlc.arg(provider) AND track_id = sqlc.arg(track_id)
  AND (state IN ('queued', 'playing') OR id = sqlc.arg(item_id));
