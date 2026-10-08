-- Songs' beat maps (MAD-773): tempo, beats, sections and the spectrum
-- from moment to moment, worked out once from the audio so every screen
-- in a room can move with the music. Songs that can't be analysed are
-- kept too, with found false, so they aren't tried on every play.

-- +goose Up
CREATE TABLE beat_maps (
    provider    TEXT NOT NULL,
    track_id    TEXT NOT NULL,
    -- analysis.Version that made it; older maps are worked out again.
    version     INTEGER NOT NULL,
    found       BOOLEAN NOT NULL,
    -- analysis.Map as JSON; '{}' when not found.
    map         TEXT NOT NULL,
    analyzed_at TIMESTAMP NOT NULL,
    -- When a room last asked for it; maps nobody plays are swept.
    used_at     TIMESTAMP NOT NULL,
    PRIMARY KEY (provider, track_id)
);

-- +goose Down
DROP TABLE beat_maps;
