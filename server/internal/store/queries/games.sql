-- name: CreateGameRound :exec
INSERT INTO game_rounds (id, room_id, queue_item_id, kind, question, started_by, started_at, revealed_at, set_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateGameAnswer :exec
INSERT INTO game_answers (round_id, user_id, answer, correct, points, answered_at)
VALUES (?, ?, ?, ?, ?, ?);

-- GameScoresSince sums each player's points in a room's rounds since a
-- time: the night's scores.
-- name: GameScoresSince :many
SELECT game_answers.user_id,
       CAST(sum(game_answers.points) AS INTEGER) AS points,
       CAST(sum(game_answers.correct) AS INTEGER) AS correct,
       count(*) AS answered
FROM game_answers
JOIN game_rounds ON game_rounds.id = game_answers.round_id
WHERE game_rounds.room_id = sqlc.arg(room_id) AND game_rounds.started_at >= sqlc.arg(since)
GROUP BY game_answers.user_id
ORDER BY points DESC, game_answers.user_id;

-- CountGameRoundsSince counts a room's rounds of some kinds since a
-- time, for the breaks-per-hour budget. A set of tunes counts once.
-- name: CountGameRoundsSince :one
SELECT count(DISTINCT coalesce(set_id, id)) FROM game_rounds
WHERE room_id = sqlc.arg(room_id) AND started_at >= sqlc.arg(since) AND kind IN (sqlc.slice(kinds));

-- HigherLowerAnswersSince lists the answers to a room's higher-or-lower
-- rounds since a time, oldest first, for the night's streaks.
-- name: HigherLowerAnswersSince :many
SELECT game_answers.user_id, game_answers.correct
FROM game_answers
JOIN game_rounds ON game_rounds.id = game_answers.round_id
WHERE game_rounds.room_id = sqlc.arg(room_id) AND game_rounds.started_at >= sqlc.arg(since)
  AND game_rounds.kind = 'year' AND json_extract(game_rounds.question, '$.Topic') = 'higher_lower'
ORDER BY game_rounds.started_at, game_rounds.id;

-- FavoriteItems lists a room's songs that played through, newest first,
-- with their hearts: what name that tune draws the room's favorites from.
-- name: FavoriteItems :many
SELECT sqlc.embed(queue_items),
       CAST((SELECT count(*) FROM hearts WHERE hearts.queue_item_id = queue_items.id) AS INTEGER) AS hearts
FROM queue_items
WHERE queue_items.room_id = sqlc.arg(room_id) AND EXISTS (
    SELECT 1 FROM play_history
    WHERE play_history.queue_item_id = queue_items.id AND play_history.end_reason = 'finished'
)
ORDER BY queue_items.added_at DESC
LIMIT sqlc.arg(limit);

-- CountHeartsFor counts the hearts on some songs: theme rounds' and
-- bracket matches' votes.
-- name: CountHeartsFor :many
SELECT queue_item_id, count(*) AS hearts FROM hearts
WHERE queue_item_id IN (sqlc.slice(ids))
GROUP BY queue_item_id;

-- LastGameRound is a room's latest kept round of a kind since a time:
-- the night's bracket, as it stood after its last match.
-- name: LastGameRound :one
SELECT * FROM game_rounds
WHERE room_id = sqlc.arg(room_id) AND kind = sqlc.arg(kind) AND started_at >= sqlc.arg(since)
ORDER BY revealed_at DESC, id DESC
LIMIT 1;
