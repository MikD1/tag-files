-- +goose Up
ALTER TABLE library_meta ADD COLUMN storage_id TEXT CHECK (storage_id IS NULL OR length(storage_id)=36);
ALTER TABLE library_meta ADD COLUMN storage_volume_id TEXT
 CHECK ((storage_id IS NULL)=(storage_volume_id IS NULL) AND (storage_volume_id IS NULL OR length(storage_volume_id)=64));
-- +goose StatementBegin
CREATE TRIGGER immutable_storage_pair BEFORE UPDATE OF storage_id,storage_volume_id ON library_meta
WHEN OLD.storage_id IS NOT NULL AND (NEW.storage_id IS NOT OLD.storage_id OR NEW.storage_volume_id IS NOT OLD.storage_volume_id)
BEGIN SELECT RAISE(ABORT,'storage identity is immutable'); END;
-- +goose StatementEnd
UPDATE library_meta SET schema_version=4 WHERE singleton=1;
PRAGMA user_version=4;
