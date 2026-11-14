-- name: CreateCleanupReceipt :one
INSERT INTO cleanup_receipts(id,reason,upload_id,payload_version,payload_json,generation,state,created_at_utc)
VALUES(?,?,?,1,?,?,'pending',?) RETURNING *;

-- name: GetCleanupReceipt :one
SELECT * FROM cleanup_receipts WHERE id=?;

-- name: FindCancellationReceipt :one
SELECT * FROM cleanup_receipts WHERE upload_id=? AND reason='cancel' ORDER BY created_at_utc,id LIMIT 1;

-- name: GetCleanupJob :one
SELECT * FROM jobs WHERE type='cleanup' AND dedupe_key=?;

-- name: ListUnpublishedUploadAssets :many
SELECT * FROM assets WHERE upload_id=? AND item_id IS NULL ORDER BY id;

-- name: DeleteUploadIngestJobs :exec
DELETE FROM jobs WHERE type='ingest' AND subject_id=?;

-- name: DeleteUploadSession :exec
DELETE FROM upload_sessions WHERE id=?;

-- name: GetCleanupAsset :one
SELECT * FROM assets WHERE id=?;

-- name: CountJobIdentity :one
SELECT COUNT(*) FROM jobs WHERE id=?;

-- name: CountWritesForKey :one
SELECT COUNT(*) FROM object_write_intents WHERE object_key=?;

-- name: DeleteCleanupReceipt :exec
DELETE FROM cleanup_receipts WHERE id=?;

-- name: DeleteCancelledAsset :exec
DELETE FROM assets WHERE id=? AND upload_id=? AND item_id IS NULL AND state<>'published';
