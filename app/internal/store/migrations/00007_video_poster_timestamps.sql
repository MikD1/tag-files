-- +goose Up
-- The previous video filter could publish its white background before the
-- first decoded frame. Retire only app-derived v1 video posters, retaining
-- their immutable bytes and ownership. Normal leased work publishes a fresh
-- verified asset; legacy posters and all primary/user metadata remain intact.
CREATE TEMP TABLE video_poster_timestamp_repairs AS
SELECT a.id AS asset_id,i.id AS item_id,i.primary_asset_id
FROM assets a JOIN items i ON i.id=a.item_id
JOIN assets source ON source.id=i.primary_asset_id
JOIN library_meta library ON library.singleton=1
WHERE i.state='ready' AND i.media_kind='Video'
 AND a.role='poster' AND a.state='published'
 AND a.provenance='derived' AND a.profile_version='poster-v1'
 AND a.bucket='tag-files'
 AND a.object_key='previews/'||library.library_id||'/'||a.id||'/poster-v1/poster.jpg'
 AND source.item_id=i.id AND source.role='primary'
 AND source.state IN ('verified','published') AND source.bucket='tag-files'
 AND source.size_bytes>0
 AND substr(source.object_key,1,length('primary/'||library.library_id||'/'||source.id||'/media.'))=
     'primary/'||library.library_id||'/'||source.id||'/media.'
 AND length(source.object_key)>length('primary/'||library.library_id||'/'||source.id||'/media.')
 AND instr(substr(source.object_key,length('primary/'||library.library_id||'/'||source.id||'/media.')+1),'/')=0;

UPDATE assets SET state='missing'
WHERE id IN (SELECT asset_id FROM video_poster_timestamp_repairs);
UPDATE items SET version=version+1
WHERE id IN (SELECT item_id FROM video_poster_timestamp_repairs);

-- Failed compatible work has no active lease. Keep its identity/generation
-- (and any old candidate cleanup receipts), while allowing the fixed worker
-- to claim a new generation. Unknown codecs and live work are never rewritten.
UPDATE jobs SET state='queued',error_code=NULL,
 next_attempt_at=strftime('%Y-%m-%dT%H:%M:%f0000Z','now')
WHERE type='poster' AND state='failed'
 AND EXISTS (SELECT 1 FROM video_poster_timestamp_repairs repair
   WHERE jobs.subject_id=CAST(repair.item_id AS TEXT)
    AND jobs.dedupe_key='poster/'||repair.primary_asset_id||'/poster-v1'
    AND json_type(jobs.payload_json,'$.version')='integer'
    AND json_extract(jobs.payload_json,'$.version')=1
    AND json_type(jobs.payload_json,'$.itemId')='integer'
    AND json_extract(jobs.payload_json,'$.itemId')=repair.item_id
    AND json_extract(jobs.payload_json,'$.primaryAssetId')=repair.primary_asset_id
    AND json_extract(jobs.payload_json,'$.profile')='poster-v1'
    AND (SELECT count(*) FROM json_each(jobs.payload_json))=4);

INSERT INTO jobs(id,type,subject_id,dedupe_key,payload_json,next_attempt_at,created_at)
SELECT lower(hex(randomblob(4))||'-'||hex(randomblob(2))||'-4'||
       substr(hex(randomblob(2)),2,3)||'-8'||substr(hex(randomblob(2)),2,3)||'-'||hex(randomblob(6))),
 'poster',CAST(repair.item_id AS TEXT),'poster/'||repair.primary_asset_id||'/poster-v1',
 json_object('version',1,'itemId',repair.item_id,'primaryAssetId',repair.primary_asset_id,'profile','poster-v1'),
 strftime('%Y-%m-%dT%H:%M:%f0000Z','now'),strftime('%Y-%m-%dT%H:%M:%f0000Z','now')
FROM (SELECT DISTINCT item_id,primary_asset_id FROM video_poster_timestamp_repairs) repair
WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE dedupe_key='poster/'||repair.primary_asset_id||'/poster-v1');

DROP TABLE video_poster_timestamp_repairs;
UPDATE library_meta SET schema_version=7 WHERE singleton=1;
PRAGMA user_version=7;
