-- name: CreateSearch :one
INSERT INTO searches (name, keywords, description, min_price, max_price, currency, location, platforms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListActiveSearches :many
SELECT * FROM searches WHERE active = 1 ORDER BY created_at DESC;

-- name: ListAllSearches :many
SELECT * FROM searches ORDER BY created_at DESC;

-- name: GetSearch :one
SELECT * FROM searches WHERE id = ?;

-- name: UpdateSearch :one
UPDATE searches
SET name = ?, keywords = ?, description = ?, min_price = ?, max_price = ?,
    currency = ?, location = ?, platforms = ?, active = ?, updated_at = unixepoch()
WHERE id = ?
RETURNING *;

-- name: DeactivateSearch :exec
UPDATE searches SET active = 0, updated_at = unixepoch() WHERE id = ?;

-- name: CountActiveSearches :one
SELECT COUNT(*) FROM searches WHERE active = 1;
