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

-- name: ListScanRunsPaged :many
SELECT sr.*, s.name AS search_name
FROM scan_runs sr
JOIN searches s ON s.id = sr.search_id
ORDER BY sr.started_at DESC
LIMIT ? OFFSET ?;

-- name: CountScanRuns :one
SELECT COUNT(*) FROM scan_runs;

-- name: ListRunningSearchIDs :many
SELECT DISTINCT search_id FROM scan_runs WHERE status = 'running';

-- name: FailStaleRuns :execresult
UPDATE scan_runs
SET status = 'failed', finished_at = unixepoch(), errors = 'interrupted: server restarted'
WHERE status = 'running';
