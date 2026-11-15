-- name: GetDeletionItem :one
SELECT * FROM items WHERE id=?;

-- name: FindItemDeletion :one
SELECT * FROM cleanup_receipts WHERE reason='delete' AND item_id=?;

-- name: GetDeletionJob :one
SELECT * FROM jobs WHERE type='delete' AND dedupe_key=?;

-- name: ListItemPosterJobs :many
SELECT * FROM jobs WHERE type='poster' AND subject_id=? ORDER BY id;

-- name: DeleteItemPosterJobs :exec
DELETE FROM jobs WHERE type='poster' AND subject_id=?;

-- name: TombstoneItem :execrows
UPDATE items SET state='deleting',version=version+1 WHERE id=? AND version=? AND state IN ('ready','missing');

-- name: CreateDeletionReceipt :one
INSERT INTO cleanup_receipts(id,reason,upload_id,item_id,payload_version,payload_json,generation,state,created_at_utc)
VALUES(?,'delete',?,?,1,?,1,'pending',?) RETURNING *;

-- name: SnapshotDeletionAssets :exec
INSERT INTO deletion_assets(receipt_id,asset_id,bucket,object_key,role,size_bytes,sha256,content_type,job_id,lease_generation)
SELECT ?,id,bucket,object_key,role,size_bytes,sha256,content_type,job_id,lease_generation FROM assets WHERE item_id=?;

-- name: SnapshotDeletionIncoming :exec
INSERT INTO deletion_assets(receipt_id,asset_id,bucket,object_key,role,lease_generation)
VALUES(?,?,'tag-files',?,'incoming',0);

-- name: CountItemAssets :one
SELECT count(*) FROM assets WHERE item_id=?;

-- name: CountDeletionAssets :one
SELECT count(*) FROM deletion_assets WHERE receipt_id=?;

-- name: ListDeletionAssets :many
SELECT * FROM deletion_assets WHERE receipt_id=? AND object_key>sqlc.arg(after_id) ORDER BY object_key LIMIT sqlc.arg(limit_count);

-- name: RecordDeletionEpoch :execrows
UPDATE deletion_assets SET absence_epoch=sqlc.arg(epoch) WHERE receipt_id=sqlc.arg(receipt_id) AND object_key=sqlc.arg(object_key)
 AND absence_epoch IS sqlc.narg(expected_epoch);

-- name: GetDeletionAsset :one
SELECT * FROM deletion_assets WHERE receipt_id=? AND object_key=?;

-- name: CountUnverifiedDeletionEpochs :one
SELECT count(*) FROM deletion_assets WHERE receipt_id=? AND (absence_epoch IS NULL OR absence_epoch=sqlc.arg(epoch));

-- name: CountDeletionDependencies :one
SELECT count(*) FROM cleanup_receipts WHERE id<>sqlc.arg(receipt_id)
 AND (item_id=sqlc.arg(item_id) OR (sqlc.narg(upload_id) IS NOT NULL AND upload_id=sqlc.narg(upload_id)));

-- name: CountUninventoriedAssets :one
SELECT count(*) FROM assets a WHERE a.item_id=sqlc.arg(item_id) AND NOT EXISTS
 (SELECT 1 FROM deletion_assets d WHERE d.receipt_id=sqlc.arg(receipt_id) AND d.asset_id=a.id AND d.object_key=a.object_key AND d.role=a.role);

-- name: DeleteTombstonedItem :execrows
DELETE FROM items WHERE id=? AND state='deleting';
