-- name: UpsertEvaluation :one
INSERT INTO evaluations (listing_id, search_id, score, reasoning, model_used)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(listing_id, search_id) DO UPDATE SET
    score      = excluded.score,
    reasoning  = excluded.reasoning,
    model_used = excluded.model_used,
    created_at = unixepoch()
RETURNING *;

-- name: GetEvaluation :one
SELECT * FROM evaluations WHERE listing_id = ? AND search_id = ?;

-- name: ListEvaluationsBySearch :many
SELECT e.*, l.title, l.url, l.price, l.currency, l.platform, l.end_time, l.status, l.condition, l.location, l.image_urls
FROM evaluations e
JOIN listings l ON l.id = e.listing_id
WHERE e.search_id = ? AND e.score >= ?
ORDER BY e.score DESC
LIMIT ?;
