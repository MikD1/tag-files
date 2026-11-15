-- +goose Up
CREATE TABLE jobs_next (
 id TEXT PRIMARY KEY NOT NULL,
 type TEXT NOT NULL CHECK(type IN ('complete','ingest','poster','cleanup','delete')),
 subject_id TEXT NOT NULL,
 dedupe_key TEXT NOT NULL UNIQUE,
 payload_json TEXT NOT NULL CHECK(json_valid(payload_json)),
 state TEXT NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','retry_wait','failed')),
 attempt INTEGER NOT NULL DEFAULT 0 CHECK(attempt>=0),
 next_attempt_at TEXT NOT NULL,
 lease_until TEXT,
 lease_generation INTEGER NOT NULL DEFAULT 0 CHECK(lease_generation>=0),
 progress_json TEXT CHECK(progress_json IS NULL OR json_valid(progress_json)),
 error_code TEXT,
 created_at TEXT NOT NULL
);
INSERT INTO jobs_next SELECT * FROM jobs;
DROP TABLE jobs;
ALTER TABLE jobs_next RENAME TO jobs;
CREATE INDEX jobs_available ON jobs(state,next_attempt_at,created_at);

CREATE TABLE operation_targets (
 operation_id TEXT PRIMARY KEY NOT NULL REFERENCES operation_results(operation_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
 target_kind TEXT NOT NULL CHECK(target_kind IN ('upload_complete','job')),
 target_id TEXT NOT NULL
);
CREATE INDEX operation_targets_subject ON operation_targets(target_kind,target_id);
-- +goose StatementBegin
CREATE TRIGGER immutable_operation_target BEFORE UPDATE ON operation_targets
BEGIN SELECT RAISE(ABORT,'operation target is immutable'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER finish_job_operations AFTER DELETE ON jobs
BEGIN
 UPDATE operation_results SET state='completed' WHERE state='pending' AND operation_id IN
 (SELECT operation_id FROM operation_targets WHERE target_kind='job' AND target_id=OLD.id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER fail_job_operations AFTER UPDATE OF state ON jobs WHEN NEW.state='failed'
BEGIN
 UPDATE operation_results SET state='failed' WHERE state='pending' AND operation_id IN
 (SELECT operation_id FROM operation_targets WHERE target_kind='job' AND target_id=NEW.id);
END;
-- +goose StatementEnd
UPDATE library_meta SET schema_version=6 WHERE singleton=1;
PRAGMA user_version=6;
