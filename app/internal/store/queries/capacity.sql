-- name: ListCapacitySessions :many
SELECT s.*,CAST(COALESCE((SELECT SUM(p.expected_size) FROM upload_parts p WHERE p.session_id=s.id AND p.state='confirmed'),0) AS INTEGER) AS confirmed_bytes
FROM upload_sessions s WHERE s.id>sqlc.arg(after_id) ORDER BY s.id LIMIT sqlc.arg(limit_count);

-- name: ListCapacityPosters :many
SELECT * FROM jobs WHERE type='poster' AND state='running' AND id>sqlc.arg(after_id) ORDER BY id LIMIT sqlc.arg(limit_count);
