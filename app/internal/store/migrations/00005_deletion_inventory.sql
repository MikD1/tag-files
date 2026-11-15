-- +goose Up
CREATE TABLE deletion_assets (
    receipt_id TEXT NOT NULL REFERENCES cleanup_receipts(id) ON DELETE CASCADE,
    asset_id TEXT NOT NULL CHECK(length(asset_id)=36),
    bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('primary','poster','incoming')),
    size_bytes INTEGER CHECK(size_bytes>=0),
    sha256 TEXT CHECK(length(sha256)=64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
    content_type TEXT,
    job_id TEXT,
    lease_generation INTEGER NOT NULL CHECK(lease_generation>=0),
    absence_epoch TEXT CHECK(absence_epoch IS NULL OR length(absence_epoch)=36),
    PRIMARY KEY(receipt_id,object_key),
    CHECK ((role='incoming' AND size_bytes IS NULL AND sha256 IS NULL AND content_type IS NULL AND job_id IS NULL AND lease_generation=0)
      OR (role<>'incoming' AND size_bytes IS NOT NULL AND sha256 IS NOT NULL AND content_type IS NOT NULL))
);
CREATE UNIQUE INDEX deletion_receipt_item ON cleanup_receipts(item_id) WHERE reason='delete';
-- +goose StatementBegin
CREATE TRIGGER immutable_deletion_identity BEFORE UPDATE OF
 receipt_id,asset_id,bucket,object_key,role,size_bytes,sha256,content_type,job_id,lease_generation ON deletion_assets
WHEN NEW.receipt_id IS NOT OLD.receipt_id OR NEW.asset_id IS NOT OLD.asset_id OR
 NEW.bucket IS NOT OLD.bucket OR NEW.object_key IS NOT OLD.object_key OR
 NEW.role IS NOT OLD.role OR NEW.size_bytes IS NOT OLD.size_bytes OR NEW.sha256 IS NOT OLD.sha256 OR
 NEW.content_type IS NOT OLD.content_type OR NEW.job_id IS NOT OLD.job_id OR NEW.lease_generation IS NOT OLD.lease_generation
BEGIN SELECT RAISE(ABORT,'deletion ownership is immutable'); END;
-- +goose StatementEnd
UPDATE library_meta SET schema_version=5 WHERE singleton=1;
PRAGMA user_version=5;
