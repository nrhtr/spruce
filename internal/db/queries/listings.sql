-- name: UpsertListing :one
INSERT INTO listings (external_id, platform, title, description, price, currency, url,
    image_urls, end_time, condition, location, raw_data, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(platform, external_id) DO UPDATE SET
    title       = excluded.title,
    description = excluded.description,
    price       = excluded.price,
    end_time    = excluded.end_time,
    status      = excluded.status,
    raw_data    = excluded.raw_data,
    last_seen   = unixepoch()
RETURNING *;

-- name: GetListing :one
SELECT * FROM listings WHERE id = ?;

-- name: GetListingByExternalID :one
SELECT * FROM listings WHERE platform = ? AND external_id = ?;

-- name: ListListingsBySearch :many
SELECT l.* FROM listings l
JOIN search_listings sl ON sl.listing_id = l.id
WHERE sl.search_id = ?
ORDER BY l.first_seen DESC
LIMIT ? OFFSET ?;

-- name: ListListingsBySearchWithScore :many
SELECT l.*, COALESCE(e.score, -1) AS eval_score
FROM listings l
JOIN search_listings sl ON sl.listing_id = l.id
LEFT JOIN evaluations e ON e.listing_id = l.id AND e.search_id = sl.search_id
WHERE sl.search_id = ?
ORDER BY eval_score DESC, l.first_seen DESC
LIMIT ? OFFSET ?;

-- name: CountListingsBySearch :one
SELECT COUNT(*) FROM listings l
JOIN search_listings sl ON sl.listing_id = l.id
WHERE sl.search_id = ?;

-- name: LinkListingToSearch :exec
INSERT INTO search_listings (search_id, listing_id) VALUES (?, ?)
ON CONFLICT DO NOTHING;

-- name: ListEndingSoon :many
SELECT * FROM listings
WHERE status = 'active'
  AND end_time IS NOT NULL
  AND end_time <= ?
ORDER BY end_time ASC;

-- name: ListNewSince :many
SELECT l.* FROM listings l
JOIN search_listings sl ON sl.listing_id = l.id
WHERE sl.search_id = ? AND l.first_seen >= ?
ORDER BY l.first_seen DESC;

-- name: CountNewToday :one
SELECT COUNT(*) FROM listings WHERE first_seen >= ?;

-- name: CountTotalListings :one
SELECT COUNT(*) FROM listings;

-- name: UpdateListingStatus :exec
UPDATE listings SET status = ?, last_seen = unixepoch() WHERE id = ?;

-- name: ListRecentListings :many
SELECT * FROM listings ORDER BY first_seen DESC LIMIT ?;
