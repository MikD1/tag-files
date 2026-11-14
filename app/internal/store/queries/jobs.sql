-- name: CreateJob :one
INSERT INTO jobs(id,type,subject_id,dedupe_key,payload_json,next_attempt_at,created_at)
VALUES(?,?,?,?,?,?,?) RETURNING *;

-- name: ClaimJob :one
UPDATE jobs SET state='running',attempt=attempt+1,lease_generation=lease_generation+1,
    lease_until=sqlc.arg(lease_until),error_code=NULL
WHERE id=(
    SELECT available.id FROM jobs AS available WHERE
      (available.state IN ('queued','retry_wait') AND available.next_attempt_at<=sqlc.arg(now)) OR
      (available.state='running' AND available.lease_until<=sqlc.arg(now))
    ORDER BY available.created_at,available.id LIMIT 1
)
RETURNING *;

-- name: GetLeasedJob :one
SELECT * FROM jobs WHERE id=sqlc.arg(id) AND lease_generation=sqlc.arg(generation) AND state='running'
  AND lease_until>sqlc.arg(now);

-- name: HeartbeatJob :execrows
UPDATE jobs SET lease_until=sqlc.arg(until)
WHERE id=sqlc.arg(id) AND lease_generation=sqlc.arg(generation) AND state='running' AND lease_until>sqlc.arg(now);

-- name: DeleteLeasedJob :execrows
DELETE FROM jobs WHERE id=sqlc.arg(id) AND lease_generation=sqlc.arg(generation) AND state='running' AND lease_until>sqlc.arg(now);

-- name: FailLeasedJob :execrows
UPDATE jobs SET state=sqlc.arg(state),next_attempt_at=sqlc.arg(next_attempt_at),
    lease_until=NULL,error_code=sqlc.arg(error_code)
WHERE id=sqlc.arg(id) AND lease_generation=sqlc.arg(generation) AND state='running' AND lease_until>sqlc.arg(now);
