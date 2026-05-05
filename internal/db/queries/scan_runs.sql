-- name: CreateScanRun :one
INSERT INTO scan_runs (search_id, platform) VALUES (?, ?)
RETURNING *;

-- name: FinishScanRun :one
UPDATE scan_runs
SET finished_at = unixepoch(), new_items = ?, errors = ?, status = ?
WHERE id = ?
RETURNING *;

-- name: ListRecentScanRuns :many
SELECT sr.*, s.name AS search_name
FROM scan_runs sr
JOIN searches s ON s.id = sr.search_id
ORDER BY sr.started_at DESC
LIMIT ?;
