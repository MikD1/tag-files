-- +goose Up
ALTER TABLE assets ADD COLUMN media_kind TEXT CHECK (media_kind IN ('Image','Video'));
ALTER TABLE assets ADD COLUMN duration_ticks INTEGER;
-- +goose StatementBegin
CREATE TRIGGER immutable_asset_descriptor BEFORE UPDATE OF content_type,width,height,rotation,media_kind,duration_ticks ON assets
WHEN NEW.content_type IS NOT OLD.content_type OR NEW.width IS NOT OLD.width OR
 NEW.height IS NOT OLD.height OR NEW.rotation IS NOT OLD.rotation OR
 NEW.media_kind IS NOT OLD.media_kind OR NEW.duration_ticks IS NOT OLD.duration_ticks
BEGIN SELECT RAISE(ABORT,'asset output descriptor is immutable'); END;
-- +goose StatementEnd
UPDATE library_meta SET schema_version=3 WHERE singleton=1;
PRAGMA user_version=3;
