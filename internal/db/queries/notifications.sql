-- name: CreateEmailNotification :one
INSERT INTO email_notifications (kind, subject, body_html, listing_ids)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetLastNotificationSentAt :one
SELECT sent_at FROM email_notifications
WHERE kind = ?
ORDER BY sent_at DESC
LIMIT 1;
