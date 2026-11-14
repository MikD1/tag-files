-- name: GetItem :one
SELECT * FROM items WHERE id=? AND state IN ('ready','missing');

-- name: GetItemTags :many
SELECT t.* FROM tags t JOIN item_tags it ON t.id=it.tag_id
WHERE it.item_id=? ORDER BY t.name COLLATE BINARY,t.id;

-- name: GetItemPoster :one
SELECT id FROM assets WHERE item_id=? AND role='poster' AND state IN ('verified','published')
ORDER BY id LIMIT 1;

-- name: SetFavorite :execrows
UPDATE items SET is_favorite=sqlc.arg(favorite),version=version+1
WHERE id=sqlc.arg(id) AND state IN ('ready','missing') AND is_favorite<>sqlc.arg(favorite);

-- name: InsertViewEvent :execrows
INSERT INTO view_events(item_id,event_id,recorded_at) VALUES(?,?,?) ON CONFLICT DO NOTHING;

-- name: IncrementView :execrows
UPDATE items SET view_count=view_count+1,last_view_at_utc=?,version=version+1
WHERE id=? AND state IN ('ready','missing');

-- name: ClearItemTags :exec
DELETE FROM item_tags WHERE item_id=?;

-- name: AddItemTag :exec
INSERT INTO item_tags(item_id,tag_id) VALUES(?,?);

-- name: SetItemCollection :execrows
UPDATE items SET collection_id=?,version=version+1 WHERE id=? AND state IN ('ready','missing');

-- name: BumpItemVersion :execrows
UPDATE items SET version=version+1 WHERE id=? AND state IN ('ready','missing');
