-- name: GetImageCache :one
SELECT data, content_type FROM image_cache WHERE url = ?;

-- name: SetImageCache :exec
INSERT INTO image_cache (url, data, content_type)
VALUES (?, ?, ?)
ON CONFLICT(url) DO NOTHING;
