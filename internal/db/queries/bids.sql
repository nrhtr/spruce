-- name: CreateBid :one
INSERT INTO bids (listing_id, amount, currency, notes)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: UpdateBidResult :one
UPDATE bids SET result = ? WHERE id = ?
RETURNING *;

-- name: GetBid :one
SELECT * FROM bids WHERE id = ?;

-- name: ListBidsByListing :many
SELECT * FROM bids WHERE listing_id = ? ORDER BY placed_at DESC;

-- name: ListAllBids :many
SELECT b.*, l.title, l.url, l.platform, l.status AS listing_status
FROM bids b
JOIN listings l ON l.id = b.listing_id
ORDER BY b.placed_at DESC
LIMIT ? OFFSET ?;

-- name: HasBidForListing :one
SELECT COUNT(*) FROM bids WHERE listing_id = ?;
