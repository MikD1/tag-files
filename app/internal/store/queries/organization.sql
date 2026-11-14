-- name: ListTags :many
SELECT * FROM tags ORDER BY name COLLATE BINARY,id;

-- name: GetTag :one
SELECT * FROM tags WHERE id=?;

-- name: CreateTag :one
INSERT INTO tags(name) VALUES(?) RETURNING *;

-- name: RenameTag :one
UPDATE tags SET name=?,version=version+1 WHERE id=? AND version=? RETURNING *;

-- name: GlobalTagStatistics :many
SELECT t.name,COUNT(i.id) AS usage_count
FROM tags t LEFT JOIN item_tags it ON t.id=it.tag_id
LEFT JOIN items i ON i.id=it.item_id AND i.state<>'deleting'
GROUP BY t.name COLLATE BINARY ORDER BY usage_count DESC,t.name COLLATE BINARY;

-- name: ListSavedSearches :many
SELECT * FROM saved_searches ORDER BY name COLLATE BINARY,id;

-- name: GetSavedSearch :one
SELECT * FROM saved_searches WHERE id=?;

-- name: CreateSavedSearch :one
INSERT INTO saved_searches(name,query_text,query_ast_json,parser_version,media_kind,validation_state)
VALUES(?,?,?,?,?,?) RETURNING *;

-- name: UpdateSavedSearch :one
UPDATE saved_searches SET name=?,query_text=?,query_ast_json=?,parser_version=?,
    media_kind=?,validation_state=?,version=version+1
WHERE id=? AND version=? RETURNING *;

-- name: DeleteSavedSearch :execrows
DELETE FROM saved_searches WHERE id=? AND version=?;

-- name: ListCollections :many
SELECT c.*,COUNT(i.id) AS item_count,
 (SELECT a.id FROM items oldest LEFT JOIN assets a ON a.item_id=oldest.id
    AND a.role='poster' AND a.state IN ('verified','published')
  WHERE oldest.collection_id=c.id AND oldest.state='ready'
  ORDER BY oldest.uploaded_at_utc,oldest.id LIMIT 1) AS cover_asset_id
FROM collections c LEFT JOIN items i ON i.collection_id=c.id AND i.state='ready'
GROUP BY c.id ORDER BY c.name COLLATE BINARY,c.id;

-- name: GetCollection :one
SELECT * FROM collections WHERE id=?;

-- name: CreateCollection :one
INSERT INTO collections(name) VALUES(?) RETURNING *;

-- name: RenameCollection :one
UPDATE collections SET name=?,version=version+1 WHERE id=? AND version=? RETURNING *;

-- name: ClearCollectionItems :exec
UPDATE items SET collection_id=NULL,version=version+1 WHERE collection_id=?;

-- name: ClearCollectionUploads :exec
UPDATE upload_sessions SET collection_id=NULL,version=version+1 WHERE collection_id=?;

-- name: DeleteCollection :execrows
DELETE FROM collections WHERE id=? AND version=?;
