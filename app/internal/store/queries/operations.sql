-- name: GetOperation :one
SELECT * FROM operation_results WHERE operation_id=?;

-- name: CreateOperation :exec
INSERT INTO operation_results(operation_id,request_hash,state,http_status,result_json,created_at,expires_at)
VALUES(?,?,?,?,?,?,?);

-- name: PruneOperations :execrows
DELETE FROM operation_results WHERE expires_at<=?;

-- name: CreateNavigationContext :exec
INSERT INTO navigation_contexts(id,origin_json,ordered_ids_json,created_at,expires_at) VALUES(?,?,?,?,?);

-- name: GetNavigationContext :one
SELECT * FROM navigation_contexts WHERE id=?;

-- name: ListLiveItemIDs :many
SELECT id FROM items WHERE state='ready' AND id IN (SELECT CAST(value AS INTEGER) FROM json_each(?));

-- name: PruneNavigationContexts :execrows
DELETE FROM navigation_contexts WHERE expires_at<=?;
