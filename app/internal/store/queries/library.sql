-- name: GetLibraryMeta :one
SELECT * FROM library_meta WHERE singleton=1;

-- name: FindReadyItemByUpload :one
SELECT * FROM items WHERE upload_id=? AND state='ready';

-- name: AdvanceSearchGeneration :exec
UPDATE library_meta SET search_generation=search_generation+1 WHERE singleton=1;
