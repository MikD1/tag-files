# Implemented library format (version 1)

This describes the new runtime foundation, not a completed importer or release.
The authoritative DDL is the numbered history in `app/internal/store/migrations`.
The external format version remains 1; current SQLite schema version is 6.
Known schemas 1–5 upgrade atomically without changing library identity/data or
an already-bound logical/physical storage pair.

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
Unknown version entries anywhere in Goose history are rejected, including an
older row hidden behind a later known version.

Schema 5 adds `deletion_assets`, an immutable, paged working snapshot of an item's
owned primary/poster keys, expected bytes and publication attempts, plus the
known incoming key/upload identity. Incoming byte facts remain NULL because
they cannot be inferred from the converted primary. The inventory references
the independent deletion receipt, not live assets, and survives item metadata
removal. A per-key absence/delete epoch is recorded before forwarding DELETE.
Only this checkpoint may change; a compare-and-swap prevents an older request
from replacing a newer lifetime. Minimal inventory/receipt/job records remain
until absent keys are rechecked at a later authenticated same-volume epoch,
then are removed together. They are temporary recovery work, not trash/history.

Schema 6 adds the durable `complete` job kind, preserving every existing job,
attempt, lease generation and payload during migration. Its versioned payload
binds upload generation and the client's ordered part-number/ETag selectors.
The worker drains/reconciles parts and verifies the complete source before
atomically replacing its leased job with ingestion work. Permanent failures
keep the session and inputs for explicit retry/cancel. `operation_targets`
links seven-day command receipts to upload publication or a job. Final ready
and failure outcomes commit with their domain transition; no session/history
row is needed to replay a ready result. Deferred foreign keys permit intent,
target and command receipt in one transaction; pruning a receipt cascades only
its link. Job retirement/failure finalizes linked accepted job-command receipts.

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

Schema 2 adds `object_write_intents`, independent of sessions/assets via opaque
target IDs rather than cascading FKs. Each live/uncertain write has an immutable
owner, target/generation, operation, key, expected storage boot epoch and versioned
operational arguments. The payload holds no file body or filename. A retry creates
a new intent. Only inflight/uncertain states exist; completed writes are removed,
and there is no expiry or terminal history. Codecs reject unknown payload versions
without deleting them. Reconciliation requires owned-result checks after proof
that the old storage lifetime cannot accept another request; a different epoch
alone does not establish success/failure. Persistent library/storage pairing and
the owned single-volume topology are prerequisites for that epoch proof.

New incoming keys are `incoming/<library UUID>/<upload UUID>/source`. The
session's opaque S3 MPU identity is attached once. After closing admission and
draining known writers, `completion_manifest` stores
`{"version":1,"parts":[{"partNumber":1,"size":7,"etag":"opaque","sha256":"base64 SHA-256"}]}`.
Entries are contiguous, ordered and carry each immutable size/digest binding;
the ETag is a multipart selector. The manifest is never replaced after freezing.
Its canonical identity SHA in a Complete write intent is distinct from the
full incoming SHA stored in `source_sha256` after HEAD/full GET verification.
The same transaction changes the session to queued and inserts the unique
`ingest/<upload UUID>` job. Its payload is
`{"version":1,"uploadId":"upload UUID","generation":1}`; that generation must
still match when a worker publishes. Repeated Complete returns the existing
queued/processing/failed or ready outcome; cancelled/missing sessions are not
recreated. Lost write replies retain independent intents even when a separate
full-read reconciliation permits the queued transition.

Cancellation uses `cleanup_receipts.reason='cancel'`, payload version 1 and
`{"version":1,"libraryId":"UUID","uploadId":"UUID","incomingKey":"owned source key","multipartId":"optional opaque S3 ID","candidates":[{"assetId":"UUID","key":"owned candidate key"}]}`.
The receipt generation identifies the removed session generation. Its job has
dedupe key `cleanup/<receipt UUID>` and payload
`{"version":1,"receiptId":"UUID"}`. Creating both records, deleting the session/
parts and removing ingest jobs is atomic. Absence of that session/job permanently
fences publication, including after lease clocks change. Cleanup only retires the
receipt/candidate metadata/job after known calls drain, owned resources are absent
and independent uncertain intents settle by the required epoch/reconciliation
proof. A failed cleanup leaves retryable working records without visible upload
history. Unknown payload versions remain preserved and block that work.

Extra owned MPUs use reason `duplicate_mpu`, payload version 1 and
`{"version":1,"libraryId":"UUID","uploadId":"UUID","key":"owned incoming key","keptMultipartId":"selected opaque ID","extraMultipartId":"different opaque ID"}`.
Selecting an MPU and recording every observed extra is one transaction. Once
attached, the selected ID never changes. The cleanup job has the same generic
receipt payload/dedupe key as cancellation work; it aborts only the recorded
extra ID and never deletes the incoming object. Repeated inventory reuses the
receipt for that upload/extra ID. These records are working cleanup, removed
with their job after verified absence, and may outlive the visible session.

Schema 3 adds nullable `assets.media_kind` and `assets.duration_ticks` for a
durable candidate output descriptor; existing asset rows retain NULL values.
The new primary factory stores Image/Video, exact nullable integer ticks,
dimensions/rotation/content type and immutable expected size/SHA before PUT.
Descriptor fields cannot be updated. Every allocation gets a new UUID and key
`primary/<library UUID>/<asset UUID>/media.<canonical extension>`, bound to the
ingest job and its lease generation. A different conversion/lease never shares
that key. Verification and publication recheck current job/session fences.

Ready publication requires a fully verified candidate. One leased transaction
creates the item/primary link, advances library generation, removes session/parts/
ingest job, and adds independent source/unused-candidate cleanup plus poster work.
Cleanup reason `ready` uses the cancellation payload plus `readyItemId` and
`primaryAssetId`; the published asset is excluded from candidate cleanup. It may
retire source/unused candidates after the permanent session/job fence and verified
removal, while preserving a ready primary and its independently uncertain PUT.
Poster job dedupe is `poster/<primary UUID>/poster-v1`; its payload is
`{"version":1,"itemId":1,"primaryAssetId":"UUID","profile":"poster-v1"}`.
Poster failure never recreates an upload. Published-primary write intents can be
reconciled after session deletion against their immutable asset and full hash.

The complete S3 keys, raw JSON payload schemas and external manifest contracts
will be documented with their implementing stages; this file does not claim
that storage, ingestion, migration, restore or UI gates have passed.
