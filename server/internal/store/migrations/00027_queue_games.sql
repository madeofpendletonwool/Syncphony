-- Queue games (MAD-794, MAD-795, MAD-796, ADR 0015). A theme round's and a
-- bracket's entries are held out of the play order until their turn, then
-- play together at the front: a theme's as a block, a bracket match's two
-- songs back to back. A night keeps its bracket, for the recap.

-- +goose Up
-- Held for a game: queued, but out of the play order until the game plays it.
ALTER TABLE queue_items ADD COLUMN game_held BOOLEAN NOT NULL DEFAULT FALSE;
-- When a game put it at the front. Such songs play after songs put back,
-- the oldest first, then the fair order. NULL for every other song.
ALTER TABLE queue_items ADD COLUMN front_at TIMESTAMP;
-- The night's bracket (games.Bracket), as JSON; '' if it had none.
ALTER TABLE nights ADD COLUMN bracket TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE nights DROP COLUMN bracket;
ALTER TABLE queue_items DROP COLUMN front_at;
ALTER TABLE queue_items DROP COLUMN game_held;
