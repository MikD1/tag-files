-- name: FindCompleteJob :one
SELECT * FROM jobs WHERE type='complete' AND dedupe_key=?;

-- name: RetryCompleteJob :execrows
UPDATE jobs SET state='queued',attempt=0,lease_until=NULL,lease_generation=lease_generation+1,
 next_attempt_at=?,error_code=NULL,progress_json=NULL WHERE id=? AND state='failed' AND lease_generation=?;

-- name: AddOperationTarget :exec
INSERT INTO operation_targets(operation_id,target_kind,target_id) VALUES(?,?,?);

-- name: PendingTargetOperations :many
SELECT r.* FROM operation_results r JOIN operation_targets t ON t.operation_id=r.operation_id
WHERE t.target_kind=sqlc.arg(target_kind) AND t.target_id=sqlc.arg(target_id)
 AND r.state='pending' AND r.operation_id>sqlc.arg(after_id) ORDER BY r.operation_id LIMIT 100;

-- name: ResolveTargetOperation :execrows
UPDATE operation_results SET state=?,http_status=?,result_json=? WHERE operation_id=? AND state='pending';
