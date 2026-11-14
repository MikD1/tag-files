-- +goose Up
CREATE TABLE object_write_intents (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(id)=36),
    owner_id TEXT NOT NULL CHECK (length(owner_id)=36),
    target_kind TEXT NOT NULL CHECK (target_kind IN ('upload','asset')),
    target_id TEXT NOT NULL CHECK (length(target_id)=36),
    generation INTEGER NOT NULL CHECK (generation>0),
    operation TEXT NOT NULL CHECK (operation IN ('create_mpu','upload_part','complete_mpu','put_object')),
    object_key TEXT NOT NULL,
    storage_epoch TEXT NOT NULL CHECK (length(storage_epoch)=36),
    payload_version INTEGER NOT NULL DEFAULT 1 CHECK (payload_version>0),
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json) AND length(payload_json)<=65536),
    state TEXT NOT NULL CHECK (state IN ('inflight','uncertain')),
    created_at_utc TEXT NOT NULL,
    CHECK ((target_kind='upload' AND operation IN ('create_mpu','upload_part','complete_mpu'))
        OR (target_kind='asset' AND operation='put_object'))
);
CREATE INDEX object_writes_target ON object_write_intents(target_kind,target_id,generation,state);
CREATE INDEX object_writes_key ON object_write_intents(object_key,state);
CREATE INDEX object_writes_owner ON object_write_intents(owner_id,state);
CREATE INDEX object_writes_epoch ON object_write_intents(storage_epoch,state);

-- +goose StatementBegin
CREATE TRIGGER immutable_object_write_intent BEFORE UPDATE OF
 id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,
 payload_version,payload_json,created_at_utc ON object_write_intents
WHEN NEW.id IS NOT OLD.id OR NEW.owner_id IS NOT OLD.owner_id OR
 NEW.target_kind IS NOT OLD.target_kind OR NEW.target_id IS NOT OLD.target_id OR
 NEW.generation IS NOT OLD.generation OR NEW.operation IS NOT OLD.operation OR
 NEW.object_key IS NOT OLD.object_key OR NEW.storage_epoch IS NOT OLD.storage_epoch OR
 NEW.payload_version IS NOT OLD.payload_version OR NEW.payload_json IS NOT OLD.payload_json OR
 NEW.created_at_utc IS NOT OLD.created_at_utc
BEGIN SELECT RAISE(ABORT,'external write intent is immutable'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER uncertain_write_cannot_restart BEFORE UPDATE OF state ON object_write_intents
WHEN OLD.state='uncertain' AND NEW.state<>'uncertain'
BEGIN SELECT RAISE(ABORT,'retry requires a new external write intent'); END;
-- +goose StatementEnd

UPDATE library_meta SET schema_version=2 WHERE singleton=1;
PRAGMA user_version=2;
