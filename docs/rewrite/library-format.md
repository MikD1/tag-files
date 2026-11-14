# Implemented library format (version 1)

This describes the new runtime foundation, not a completed importer or release.
The authoritative DDL is `app/internal/store/migrations/00001_library.sql`.

App data contains `library.sqlite`, its WAL/SHM when present, and `.library.lock`.
The application holds an OS advisory exclusive lock for its entire lifetime.
External tools must require a stopped app and acquire the same lock before
opening a target library. Cold backup also requires stopped storage. A lock
filename alone never proves a process is running or stopped.

`PRAGMA user_version`, `library_meta.schema_version` and the applied Goose
version must agree. `library_meta.format_version` controls external format
compatibility; `library_id` is a persistent random UUID. Unknown/future versions
and legacy EF databases are rejected before migrations or journal-mode writes.
The initial migration is atomic; an interrupted empty Goose install resumes.

The only application writer has one connection and BEGIN IMMEDIATE. Four
read-only/query-only connections serve readers. Each physical connection has
foreign keys, synchronous FULL and a 5000 ms busy timeout; journal mode is WAL.

Public entity IDs are positive INTEGERs no larger than 9007199254740991; tags
have BINARY names without uniqueness. Version counters start at one. Legacy
duration is nullable INTEGER ticks (100 ns); the full signed int64 survives
storage exactly, including values that cannot safely pass through JSON number
or Go nanosecond durations. Import validation of historical invalid values is
separate from preservation of their raw representation in the migration archive.
UTC text timestamps use exactly seven fractional digits and `Z`; the external
importer must archive source timestamps before any explicit interpretation.

Ready items require a verified owned primary. Asset keys, expected size and
SHA-256 are immutable from candidate creation. Library metadata owns references;
an ETag is never a SHA-256. Object and process IO occur outside SQL transactions.

Upload sessions hold only unfinished/error work and have no expiry. Part
size/digest bindings are immutable. `items.upload_id` is nullable UNIQUE and
has no FK to sessions. Cleanup receipts contain independent, versioned JSON
payloads, not cascading session FKs. Successful jobs are removed atomically
with their result; failed jobs retain their state and are not reclaimed by age.
Claims increment lease generations, expired workers cannot heartbeat or commit,
and a failed publication transaction rolls back both metadata and job removal.

The complete S3 keys, raw JSON payload schemas and external manifest contracts
will be documented with their implementing stages; this file does not claim
that storage, ingestion, migration, restore or UI gates have passed.
