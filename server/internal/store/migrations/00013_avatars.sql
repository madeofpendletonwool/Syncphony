-- Uploaded profile pictures (MAD-724). The server crops and shrinks them
-- before saving, so each is a few tens of KB. users.avatar points at
-- /api/users/{id}/avatar while one is set.

-- +goose Up
CREATE TABLE avatars (
    user_id      TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    data         BLOB NOT NULL,
    content_type TEXT NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE avatars;
