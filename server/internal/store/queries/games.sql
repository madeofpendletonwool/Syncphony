-- name: CreateGameRound :exec
INSERT INTO game_rounds (id, room_id, queue_item_id, kind, question, started_by, started_at, revealed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

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
-- time, for the breaks-per-hour budget.
-- name: CountGameRoundsSince :one
SELECT count(*) FROM game_rounds
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
