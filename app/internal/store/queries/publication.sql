-- name: CreatePrimaryCandidate :one
INSERT INTO assets(id,upload_id,job_id,lease_generation,role,bucket,object_key,size_bytes,sha256,
 content_type,width,height,rotation,provenance,state,media_kind,duration_ticks)
VALUES(?,?,?,?,'primary','tag-files',?,?,?,?,?,?,?,'upload','candidate',?,?) RETURNING *;

-- name: MarkUploadProcessing :exec
UPDATE upload_sessions SET state='processing',version=version+1 WHERE id=? AND state='queued';

-- name: MarkPrimaryVerified :exec
UPDATE assets SET state='verified' WHERE id=?;

-- name: CreatePublishedItem :one
INSERT INTO items(upload_id,media_kind,uploaded_at_utc,collection_id,duration_ticks,state)
VALUES(?,?,?,?,?,'importing') RETURNING *;

-- name: AttachPublishedPrimary :exec
UPDATE assets SET item_id=?,state='published' WHERE id=?;

-- name: MarkItemReady :exec
UPDATE items SET primary_asset_id=?,state='ready' WHERE id=?;
