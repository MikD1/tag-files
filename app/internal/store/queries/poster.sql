-- name: CreatePosterCandidate :one
INSERT INTO assets(id,upload_id,job_id,lease_generation,role,bucket,object_key,size_bytes,sha256,
 content_type,width,height,rotation,provenance,profile_version,state,media_kind)
VALUES(?,?,?,?,'poster','tag-files',?,?,?,'image/jpeg',?,?,0,'derived',?,'candidate','Image') RETURNING *;

-- name: CreatePosterCandidateReceipt :one
INSERT INTO cleanup_receipts(id,reason,item_id,payload_version,payload_json,generation,state,created_at_utc)
VALUES(?,'poster_candidate',?,1,?,?,'pending',?) RETURNING *;

-- name: FindPosterCandidateReceipt :one
SELECT * FROM cleanup_receipts WHERE reason='poster_candidate'
 AND json_extract(payload_json,'$.assetId')=? ORDER BY id LIMIT 1;

-- name: DeleteReceiptJob :exec
DELETE FROM jobs WHERE type='cleanup' AND subject_id=?;

-- name: MarkPosterVerified :execrows
UPDATE assets SET state='verified' WHERE id=? AND role='poster' AND item_id IS NULL AND state IN ('candidate','verified');

-- name: AttachPublishedPoster :execrows
UPDATE assets SET item_id=?,state='published' WHERE id=? AND role='poster' AND item_id IS NULL AND state='verified';

-- name: GetPublishedPoster :one
SELECT * FROM assets WHERE item_id=? AND role='poster' AND state='published' AND profile_version=? ORDER BY id LIMIT 1;

-- name: DeleteOrphanPoster :execrows
DELETE FROM assets WHERE id=? AND role='poster' AND item_id IS NULL AND state IN ('candidate','verified');
