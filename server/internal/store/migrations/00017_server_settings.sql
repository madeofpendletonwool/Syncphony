-- Server-wide settings that don't belong to a room.

-- +goose Up

CREATE TABLE server_settings (
    -- One row.
    id       INTEGER PRIMARY KEY CHECK (id = 1),
    -- admin.Settings as JSON.
    settings TEXT NOT NULL
);

-- +goose Down

DROP TABLE server_settings;
