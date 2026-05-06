-- name: CreateMagicLink :exec
INSERT INTO magic_links (token, expires_at) VALUES (?, unixepoch() + 900);

-- name: VerifyMagicLink :execresult
UPDATE magic_links SET used = 1
WHERE token = ? AND used = 0 AND expires_at > unixepoch();

-- name: CreateSession :exec
INSERT INTO sessions (token, expires_at) VALUES (?, unixepoch() + 86400);

-- name: VerifySession :one
SELECT COUNT(*) FROM sessions WHERE token = ? AND expires_at > unixepoch();
