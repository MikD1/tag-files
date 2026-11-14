-- name: CreateObjectWriteIntent :one
INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,
 object_key,storage_epoch,payload_version,payload_json,state,created_at_utc)
VALUES(?,?,?,?,?,?,?,?,1,?,'inflight',?) RETURNING *;

-- name: GetObjectWriteIntent :one
SELECT * FROM object_write_intents WHERE id=?;

-- name: ListObjectWriteIntents :many
SELECT * FROM object_write_intents WHERE id>sqlc.arg(after_id)
ORDER BY id LIMIT sqlc.arg(limit_count);

-- name: MarkObjectWriteUncertain :execrows
UPDATE object_write_intents SET state='uncertain'
WHERE id=? AND owner_id=? AND state='inflight';

-- name: DeleteSettledObjectWrite :execrows
DELETE FROM object_write_intents WHERE id=? AND owner_id=? AND storage_epoch=?;
