-- Name that tune sets (MAD-793, ADR 0015): a host starts a set of tunes
-- that run back to back with the music stopped. Each tune is a round; the
-- set they belong to counts once toward the hour's breaks.

-- +goose Up
ALTER TABLE game_rounds ADD COLUMN set_id TEXT;

-- +goose Down
ALTER TABLE game_rounds DROP COLUMN set_id;
