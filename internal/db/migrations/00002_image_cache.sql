-- +goose Up
CREATE TABLE image_cache (
    url          TEXT    NOT NULL PRIMARY KEY,
    data         BLOB    NOT NULL,
    content_type TEXT    NOT NULL DEFAULT 'image/jpeg',
    fetched_at   INTEGER NOT NULL DEFAULT (unixepoch())
);

-- +goose Down
DROP TABLE image_cache;
