-- name: GetMediaAsset :one
SELECT a.*,i.id AS owner_id,i.media_kind AS item_media_kind,i.primary_asset_id AS current_primary,i.state AS owner_state
FROM assets a JOIN items i ON i.id=a.item_id WHERE a.id=? AND a.state IN ('verified','published')
 AND i.state IN ('ready','missing') AND (a.role='poster' OR (a.role='primary' AND i.state='ready' AND i.primary_asset_id=a.id));

-- name: MarkPrimaryMissing :execrows
UPDATE items SET state='missing',version=version+1
WHERE id=? AND state='ready' AND primary_asset_id=?;

-- name: MarkMediaAssetMissing :execrows
UPDATE assets SET state='missing' WHERE id=? AND item_id=? AND state IN ('verified','published');
