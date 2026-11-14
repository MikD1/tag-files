-- name: CreateUploadSession :one
INSERT INTO upload_sessions(id,batch_id,client_file_id,client_filename,fingerprint,
 expected_size,part_size,part_count,incoming_key,collection_id,state,created_at_utc)
VALUES(?,?,?,?,?,?,?,?,?,?,'creating',?) RETURNING *;

-- name: GetUploadSession :one
SELECT * FROM upload_sessions WHERE id=?;

-- name: ListUploadSessions :many
SELECT * FROM upload_sessions ORDER BY created_at_utc,id;

-- name: AttachUploadMultipart :execrows
UPDATE upload_sessions SET multipart_upload_id=?,state='receiving',version=version+1
WHERE id=? AND generation=? AND state='creating' AND multipart_upload_id IS NULL;

-- name: ListUploadParts :many
SELECT * FROM upload_parts WHERE session_id=? ORDER BY part_number;

-- name: GetUploadPart :one
SELECT * FROM upload_parts WHERE session_id=? AND part_number=?;

-- name: BindUploadPart :exec
INSERT INTO upload_parts(session_id,part_number,expected_size,sha256,state)
VALUES(?,?,?,?,'bound') ON CONFLICT(session_id,part_number) DO NOTHING;

-- name: GetIngestionJob :one
SELECT * FROM jobs WHERE dedupe_key=? AND type='ingest';

-- name: CountUploadCreateIntents :one
SELECT COUNT(*) FROM object_write_intents
WHERE target_kind='upload' AND target_id=? AND operation='create_mpu';

-- name: ConfirmUploadPart :exec
UPDATE upload_parts SET etag=?,state='confirmed' WHERE session_id=? AND part_number=?;

-- name: ResetUploadPartConfirmation :exec
UPDATE upload_parts SET etag=NULL,state='bound' WHERE session_id=? AND part_number=?;

-- name: BumpUploadVersion :exec
UPDATE upload_sessions SET version=version+1 WHERE id=?;

-- name: CountUploadCompleteIntents :one
SELECT COUNT(*) FROM object_write_intents
WHERE target_kind='upload' AND target_id=? AND operation='complete_mpu';

-- name: CloseUploadAdmission :exec
UPDATE upload_sessions SET state='completing',version=version+1
WHERE id=? AND state='receiving';

-- name: ReopenUploadAdmission :exec
UPDATE upload_sessions SET state='receiving',version=version+1
WHERE id=? AND state='completing' AND completion_manifest IS NULL;

-- name: FreezeUploadManifest :exec
UPDATE upload_sessions SET completion_manifest=?,version=version+1 WHERE id=?;

-- name: QueueVerifiedUpload :exec
UPDATE upload_sessions SET source_sha256=?,state='queued',version=version+1,error_code=NULL WHERE id=?;
