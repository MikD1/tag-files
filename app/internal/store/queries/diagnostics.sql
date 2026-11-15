-- name: CountJobsByState :many
SELECT state,COUNT(*) AS count FROM jobs GROUP BY state ORDER BY state;
