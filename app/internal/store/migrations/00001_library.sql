-- +goose Up
PRAGMA user_version = 1;

CREATE TABLE library_meta (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    library_id TEXT NOT NULL UNIQUE CHECK (length(library_id) = 36),
    schema_version INTEGER NOT NULL,
    format_version INTEGER NOT NULL,
    search_generation INTEGER NOT NULL DEFAULT 0 CHECK (search_generation >= 0)
);
INSERT INTO library_meta VALUES (1,
    lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' ||
          substr(hex(randomblob(2)), 2, 3) || '-8' ||
          substr(hex(randomblob(2)), 2, 3) || '-' || hex(randomblob(6))), 1, 1, 0);

CREATE TABLE collections (
    id INTEGER PRIMARY KEY AUTOINCREMENT CHECK (id BETWEEN 1 AND 9007199254740991),
    name TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE TABLE tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT CHECK (id BETWEEN 1 AND 9007199254740991),
    name TEXT NOT NULL COLLATE BINARY,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE INDEX tags_name ON tags(name COLLATE BINARY);

CREATE TABLE items (
    id INTEGER PRIMARY KEY AUTOINCREMENT CHECK (id BETWEEN 1 AND 9007199254740991),
    upload_id TEXT UNIQUE,
    description TEXT,
    media_kind TEXT NOT NULL CHECK (media_kind IN ('Unknown','Text','Image','Video','Audio')),
    uploaded_at_utc TEXT NOT NULL,
    collection_id INTEGER REFERENCES collections(id) ON DELETE SET NULL,
    is_favorite INTEGER NOT NULL DEFAULT 0 CHECK (is_favorite IN (0,1)),
    view_count INTEGER NOT NULL DEFAULT 0 CHECK (view_count BETWEEN 0 AND 9007199254740991),
    last_view_at_utc TEXT,
    duration_ticks INTEGER,
    primary_asset_id TEXT REFERENCES assets(id) DEFERRABLE INITIALLY DEFERRED,
    state TEXT NOT NULL CHECK (state IN ('importing','ready','deleting','missing')),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);
CREATE INDEX items_uploaded ON items(state,uploaded_at_utc,id);
CREATE INDEX items_duration ON items(state,duration_ticks,id);
CREATE INDEX items_views ON items(state,view_count,id);
CREATE INDEX items_collection ON items(collection_id,state,uploaded_at_utc,id);
CREATE INDEX items_favorite ON items(state,is_favorite,id);

CREATE TABLE item_tags (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY(item_id,tag_id)
);
CREATE INDEX item_tags_tag ON item_tags(tag_id,item_id);

CREATE TABLE saved_searches (
    id INTEGER PRIMARY KEY AUTOINCREMENT CHECK (id BETWEEN 1 AND 9007199254740991),
    name TEXT NOT NULL,
    query_text TEXT NOT NULL,
    legacy_source_text TEXT,
    query_ast_json TEXT NOT NULL CHECK (json_valid(query_ast_json)),
    parser_version INTEGER NOT NULL,
    media_kind TEXT CHECK (media_kind IN ('Unknown','Text','Image','Video','Audio')),
    validation_state TEXT NOT NULL CHECK (validation_state IN ('valid','unresolved','needs_review')),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0)
);

CREATE TABLE assets (
    id TEXT PRIMARY KEY NOT NULL,
    item_id INTEGER REFERENCES items(id) ON DELETE CASCADE,
    upload_id TEXT,
    job_id TEXT,
    lease_generation INTEGER NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    role TEXT NOT NULL CHECK (role IN ('primary','poster')),
    bucket TEXT NOT NULL,
    object_key TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
    content_type TEXT NOT NULL,
    width INTEGER CHECK (width > 0),
    height INTEGER CHECK (height > 0),
    rotation INTEGER,
    provenance TEXT NOT NULL,
    profile_version TEXT,
    state TEXT NOT NULL CHECK (state IN ('candidate','verified','published','missing')),
    UNIQUE(bucket,object_key)
);
CREATE INDEX assets_item ON assets(item_id,role,state);

-- +goose StatementBegin
CREATE TRIGGER ready_item_insert BEFORE INSERT ON items
WHEN NEW.state='ready' AND NOT EXISTS (
    SELECT 1 FROM assets WHERE id=NEW.primary_asset_id AND item_id=NEW.id
      AND role='primary' AND state IN ('verified','published'))
BEGIN SELECT RAISE(ABORT,'ready item requires verified owned primary'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER ready_item_update BEFORE UPDATE ON items
WHEN NEW.state='ready' AND NOT EXISTS (
    SELECT 1 FROM assets WHERE id=NEW.primary_asset_id AND item_id=NEW.id
      AND role='primary' AND state IN ('verified','published'))
BEGIN SELECT RAISE(ABORT,'ready item requires verified owned primary'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER immutable_asset_bytes BEFORE UPDATE OF bucket,object_key,size_bytes,sha256 ON assets
WHEN NEW.bucket<>OLD.bucket OR NEW.object_key<>OLD.object_key OR
     NEW.size_bytes<>OLD.size_bytes OR NEW.sha256<>OLD.sha256
BEGIN SELECT RAISE(ABORT,'asset bytes and key are immutable'); END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER protect_ready_primary BEFORE UPDATE OF item_id,role,state ON assets
WHEN EXISTS (SELECT 1 FROM items WHERE primary_asset_id=OLD.id AND state='ready')
  AND (NEW.item_id IS NOT OLD.item_id OR NEW.role<>'primary' OR
       NEW.state NOT IN ('verified','published'))
BEGIN SELECT RAISE(ABORT,'transition item out of ready before invalidating primary'); END;
-- +goose StatementEnd

CREATE TABLE upload_sessions (
    id TEXT PRIMARY KEY NOT NULL,
    batch_id TEXT NOT NULL,
    client_file_id TEXT NOT NULL,
    client_filename TEXT NOT NULL,
    fingerprint TEXT,
    expected_size INTEGER NOT NULL CHECK (expected_size > 0),
    part_size INTEGER NOT NULL CHECK (part_size BETWEEN 5242880 AND 5368709120),
    part_count INTEGER NOT NULL CHECK (part_count BETWEEN 1 AND 10000),
    incoming_key TEXT NOT NULL UNIQUE,
    multipart_upload_id TEXT,
    completion_manifest TEXT CHECK (completion_manifest IS NULL OR json_valid(completion_manifest)),
    source_sha256 TEXT,
    collection_id INTEGER REFERENCES collections(id) ON DELETE SET NULL,
    state TEXT NOT NULL CHECK (state IN ('creating','receiving','completing','queued','processing','failed')),
    generation INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    error_code TEXT,
    created_at_utc TEXT NOT NULL,
    UNIQUE(batch_id,client_file_id)
);
CREATE INDEX upload_sessions_batch ON upload_sessions(batch_id);
CREATE TABLE upload_parts (
    session_id TEXT NOT NULL REFERENCES upload_sessions(id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL CHECK (part_number BETWEEN 1 AND 10000),
    expected_size INTEGER NOT NULL CHECK (expected_size BETWEEN 1 AND 5368709120),
    sha256 TEXT NOT NULL,
    etag TEXT,
    state TEXT NOT NULL CHECK (state IN ('bound','confirmed')),
    PRIMARY KEY(session_id,part_number)
);
-- +goose StatementBegin
CREATE TRIGGER immutable_part_binding BEFORE UPDATE OF expected_size,sha256 ON upload_parts
WHEN NEW.expected_size<>OLD.expected_size OR NEW.sha256<>OLD.sha256
BEGIN SELECT RAISE(ABORT,'part binding is immutable'); END;
-- +goose StatementEnd

CREATE TABLE jobs (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('ingest','poster','cleanup','delete')),
    subject_id TEXT NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','retry_wait','failed')),
    attempt INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    next_attempt_at TEXT NOT NULL,
    lease_until TEXT,
    lease_generation INTEGER NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    progress_json TEXT CHECK (progress_json IS NULL OR json_valid(progress_json)),
    error_code TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX jobs_available ON jobs(state,next_attempt_at,created_at);
CREATE TABLE cleanup_receipts (
    id TEXT PRIMARY KEY NOT NULL,
    reason TEXT NOT NULL,
    upload_id TEXT,
    item_id INTEGER,
    payload_version INTEGER NOT NULL DEFAULT 1,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    generation INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending','reconciling','failed')),
    created_at_utc TEXT NOT NULL
);
CREATE TABLE view_events (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    PRIMARY KEY(item_id,event_id)
);
CREATE TABLE operation_results (
    operation_id TEXT PRIMARY KEY NOT NULL,
    request_hash TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending','completed','failed')),
    http_status INTEGER NOT NULL,
    result_json TEXT NOT NULL CHECK (json_valid(result_json)),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX operations_expiry ON operation_results(expires_at);
CREATE TABLE navigation_contexts (
    id TEXT PRIMARY KEY NOT NULL,
    origin_json TEXT NOT NULL CHECK (json_valid(origin_json)),
    ordered_ids_json TEXT NOT NULL CHECK (json_valid(ordered_ids_json)),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

