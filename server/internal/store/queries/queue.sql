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
