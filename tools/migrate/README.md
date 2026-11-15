# Offline legacy migration

This standalone Go module implements Task 7. It runs while the old and new apps
are stopped, never modifies source SQLite or source S3, and never converts media.
It is not linked into the app, exposed over HTTP, or included in its executable.
It deliberately reuses the production SQLite driver/Goose schema, S3 adapter,
storage pairing/epoch protocol, process supervisor and search AST implementation.
The unusual module prefix allows Go's internal-package rule to enforce that reuse
without moving the executable inside the application tree.

Read `../../docs/rewrite/04-migration-and-operations.md` before an actual run.
No real library has been scanned or migrated in this development session.

## Commands

From the repository root:

```sh
cd tools/migrate
go build -trimpath -o /tmp/tag-files-migrate ./cmd/tag-files-migrate
/tmp/tag-files-migrate scan -config /private/migration.json
/tmp/tag-files-migrate probe -config /private/migration.json > /private/probe.json
/tmp/tag-files-migrate copy -config /private/migration.json
/tmp/tag-files-migrate verify -config /private/migration.json
```

Use Linux for these tools. `probe` requires `ffprobe` (the tested host version is
recorded in its report). `copy` and `verify` acquire the production metadata
ownership lock, so an app process cannot open the same target concurrently.
There is no command that starts applications, switches deployment or deletes
source/archive/old volumes. A successful `copy` still requires `verify`.

Example configuration (credential files must be regular, private files, at most
4096 bytes, with one nonempty value and optional final newline):

```json
{
  "Source": {
    "Endpoint": "http://source-minio:9000",
    "Region": "us-east-1",
    "Buckets": ["library", "temporary", "thumbnail"],
    "AccessKeyFile": "/run/secrets/source_access",
    "SecretKeyFile": "/run/secrets/source_secret"
  },
  "Database": "/source/app.sqlite",
  "Archive": "/archive/run-001",
  "SnapshotID": "owner-frozen-snapshot-001",
  "RollbackReference": "verified cold legacy snapshot and original deployment",
  "RollbackVerified": true,
  "ArchiveReserveBytes": 10000000000,
  "Target": {
    "Endpoint": "http://new-owned-storage:8333",
    "Region": "us-east-1",
    "AccessKeyFile": "/run/secrets/target_access",
    "SecretKeyFile": "/run/secrets/target_secret"
  },
  "TargetMetadata": "/metadata",
  "Journal": "/journal/migration.sqlite",
  "Budget": {
    "writePeakBytes": 2147483648,
    "metadataBytes": 1073741824,
    "reserveBytes": 0
  },
  "PartSize": 16777216,
  "SinglePutLimit": 16777216,
  "Exceptions": [],
  "FFprobe": "ffprobe"
}
```

The budget numbers above are examples, not measured sizing claims. Replace W
(`writePeakBytes`) and D (`metadataBytes`) with the pilot's measured values.
Zero reserve selects `max(10 GB, 10% total planned target bytes)`. The serial
copy gates every object on remaining bytes + W + D + fixed R, plus inode capacity.
On resume, an existing mapped key earns payload-space credit only after HEAD,
full destination SHA256/size and Content-Type/user-metadata verification; journal
state and ETag alone earn no credit. This also permits a completed replay without
requiring a second payload-sized allocation.
The authenticated owned gateway supplies destination capacity. Archive and
journal capacity are checked locally. Host capacity, Docker VM/disk-image limits,
shared backing filesystems, scratch and any full-size rehearsal/rollback copies
still require the operator's separate physical-disk calculation from section 5.4.
Unknown capacity stops copying. No source bytes are counted as free space.

The four source/archive/target/journal directories must be disjoint, including
resolved symlink ancestors; source and target S3 origins must differ. Supply only
read-only source credentials and mount frozen source SQLite read-only. The source
adapter exposes only listing/GET/HEAD calls. It refuses any bucket visible to
ListBuckets that is omitted from the explicit bucket list. The target must be a
new, empty production owned-storage volume, already passing its persisted global
fsync/private-bucket preflight. The CLI binds the new SQLite/storage pair and
pins every request to its authenticated persistent identities and boot epoch.
It does not mount or write SeaweedFS `/data`.

## Archive and checkpoints

`scan` requires a new archive directory, a frozen snapshot ID and an explicit
verified rollback reference. It writes:

- `native/legacy.sqlite` and any WAL/SHM/hot-journal files, copied independently;
- `snapshot.sqlite`, a recovered, coherent copy made only in private work space;
- `rows.jsonl`, every table (including schema, EF history and ProcessingFiles),
  with SQLite `typeof`, decimal INTEGER tokens and hex TEXT/BLOB bytes;
- `objects/<SHA256-of-source-identity>`, complete independent bytes for every
  visible object version in every listed bucket, including temporary/orphans;
- `manifest.json`, raw mappings, migrations, SQLite checks, exact S3 keys,
  versions/delete markers/headers/user metadata/tags/bucket configuration,
  MPU descriptors, typed model, issues and source fingerprint;
- `complete`, the SHA256 of the final manifest, written and synced last.

Text/blob hex and the retained native database preserve malformed UTF-8 and raw
values even when they cannot enter the new schema. Source files are only opened
for OS reads. WAL recovery, VACUUM INTO and any SQLite index writes occur on an
independent working copy inside the archive. Only the tool's own temporary work
copy is removed. An interrupted scan has no complete marker and is not accepted;
start a new archive generation. Target copy/resume is independent and durable.

`LoadArchive` rehashes every archived file, rejects unsupported formats, unsafe
paths and symlinks. A frozen-source check rereads native files, all object/bucket
metadata and **every source object's SHA256**, including historical versions;
ETag is never accepted as content identity. These full passes deliberately add
I/O to scan, copy, resume and verification; offline-window measurements must
include them.

The separate WAL/FULL/foreign-key/immediate-transaction SQLite journal persists
one immutable source/target/library/schema/exception/transfer-geometry binding,
object/asset mappings, states, attempts/errors, multipart IDs/part digests/ETags,
and row mappings. It has its own nonblocking process lock and version marker;
unrecognized SQLite files are refused before journal-mode/schema writes.
Each existing target key is fully hashed before reuse. A mismatch stops; it is
never overwritten or hidden behind a new mapping. Different items sharing a
legacy key get separate owned primary copies. Non-library objects and old
versions get independent quarantine keys as well as their archive copy.

Large objects use a bounded section reader, immutable part hashes committed
before dispatch and the production SDK's multipart protocol. Resume reconciles
parts against those bindings, including lost acknowledgements. A successful
Complete is followed by HEAD/full GET/hash before the catalog transaction.
Lost successful PUT/Complete replies are recovered through full key verification.
Every Create attempt persists its epoch in the WAL/FULL journal before dispatch,
including retries after a restart. An uncertain Create with neither a persisted
ID nor a listed MPU is not repeated at the same storage epoch. Restarting the
isolated target storage gives a new epoch barrier; resume then reconciles before
a new attempt. A second uncertain response is fenced at that new epoch as well.
An unavailable epoch never proves a restart barrier. Ambiguous
multiple owned MPUs or unbound/conflicting parts stop for explicit investigation.
The tool never performs network I/O inside the catalog SQL transaction.

Capacity credit applies to complete objects proven by full GET/hash and bounded
preflight of unfinished MPU parts. Each credited part must have its immutable
binding persisted before dispatch, exact geometry and a freshly hashed source
archive section. Authoritative ListParts must match its number/size/ETag and
either return the exact canonical SHA256 or match a separately journaled,
SDK-validated UploadPart acknowledgement for that upload ID/size/SHA256/ETag.
SeaweedFS 4.53 omits ListParts checksums; the validated ACK path supports that
native backend without treating an ETag as a content hash. Credited parts must
remain identical on the next ListParts before any new part dispatch.

The additive `part_acknowledgements` journal table is written only after an
actually validated UploadPart reply. Existing `parts.etag` values are never
upgraded into proof: older tools could write them during checksum-less
reconciliation. A lost ACK without an authoritative ListParts checksum therefore
earns no part credit. If remaining space is insufficient, the budget stop names
the missing proof; source, journal and MPU remain intact. Conflicting,
noncanonical or unbound parts stop before writes. The complete W+D+R budget is
measured again immediately before Complete; working space and R are never spent
as optimistic credit. This is logical payload accounting, not a substitute for
measuring representative backend working peaks.

## Values, provenance and exceptions

All five legacy kinds, IDs, duplicate/case-sensitive Unicode tag names, nullable
values, joins, descriptions, counters/favorites and exact int64 100 ns ticks are
preserved. Known .NET TimeSpan text is parsed with integer arithmetic, including
days, negatives and int64 limits; unknown formats are reported. Timestamps retain
raw text and normalize known UTC forms to the production fixed-seven-digit tick
format, preserving chronological TEXT sorting. Sub-tick timestamps are refused
rather than rounded. No media title or runtime upload/job history is invented.
ProcessingFiles is archived in full and is never automatically executed.

Per-item reports retain the primary asset/source key/version/hash, conservative
`legacy_unknown` / `legacy_copied` / `legacy_transcoded` provenance and original
availability with evidence. Copied/transcoded provenance requires a unique
completed processing record, matching kind/owner and matching S3 `item-id`.
A same-name temporary object alone never proves original identity. Existing video
bytes are copied unchanged. The archive always retains historical processing
filenames even when their meaning is uncertain.

The actual C# category converter recognizes ASCII query terms only and silently
discards unmatched characters, despite Unicode tag names being legal. Its `!`
negates only an immediately adjacent term: legacy `! Cat` means positive `Cat`.
This tool uses that exact regex tokenization and an independent boolean parser,
and blocks every skipped non-whitespace byte rather than guessing intent. This
includes separated/extra `!`, lone/extra `&` or `|`, Unicode and punctuation.
Owner-approved `archive_only` retains these raw categories without creating a
different saved query. Invalid token sequences and target expression limits are
also blocking. Accepted ASCII queries bind all duplicate IDs and compare
source-name evaluation with target AST SQL item ID sets. Tests independently
project the C# converter to SQL over original legacy tables, including known
source IDs for skipped-token cases. Raw queries remain in the archive and
`legacy_source_text` for valid imported categories.

Blocking issues prevent **all target object writes** until named exceptions are
provided. Each exception contains `issueId`, `decision`, `reason` and `owner`:

- `missing_primary`: `missing_media` preserves the item metadata as `missing`;
- `dangling_collection`: `clear_reference` explicitly nulls that reference;
- other malformed rows/joins/categories: `archive_only`, retaining their raw
  rows and explicit archived row mappings;
- source multipart remnants: `archive_only` accepts the explicit dependency on
  the preserved native legacy rollback snapshot for uncommitted source parts.

Unknown source schemas remain blocked even after a generic exception: use a
reviewed matching tool. Exceptions are immutable once target copying starts.
Do not edit an issue into success or reuse another owner's issue IDs. Accepted
exceptions remain visible in the final verification report. Missing posters and
archived processing/quarantine entries are reported without executing old work.
The target's poster recovery can rebuild a missing derived poster from primary.

## Verification and switching

`verify` requires the existing journal, hashes all target objects again, verifies
Content-Type/user metadata, exact projected scalars/IDs/joins/counts and target
foreign keys, and compares every valid category's visible item ID set. Accepted missing-media
exceptions retain their source IDs in the report while target IDs reflect the
catalog's ready-only visibility; such differences remain explicit accepted losses. It stores a new
immutable `verify-<run>-<timestamp-hash>.json` beside the journal. Source S3 tags
and ancillary headers remain exact in the archive; target media carries its
original Content-Type and user metadata. Target keys do not inherit legacy
`item-id` tags or arbitrary cache/disposition headers.

`complete: true` means the data projection and accepted exceptions passed these
checks. It is **not** release/cutover approval. `probe` separately opens every
legacy Image/Video primary with bounded FFprobe output/time, protocol restrictions
and the production process-group supervisor; it preserves corrupt bytes and
reports each failure. Archive its JSON report outside the immutable archive.
Probe failures require owner review. Probe does not replace full hash checks or
promise a full decode, player compatibility or successful browser seeking.

Before cutover, close the tool; run external SQLite mode=ro and S3
HEAD/GET/Range checks on the verified pair while the app stays stopped. Use only
an isolated disposable copy for app startup/recovery/browser acceptance, including
all kinds, categories/rename, collections, favorites, six sorts and seeking.
Keep the verified original target pair frozen until switching. Measure the full
pilot/offline window and W/RTO; keep source, archive, journal, config/secrets and
previous deployment for rollback. Stop gates include changed source, checksum
mismatch, unaccepted loss/ambiguity, insufficient physical space and expired
maintenance window. Before new writes, stop new services and return to the
frozen legacy deployment. After new writes, returning to the old snapshot loses
the new delta; preserve the complete new native pair and repair/restore it or use
a separately verified reverse-migration tool. No such reverse tool is implied.

## Verification evidence (2026-11-15)

```sh
cd tools/migrate
go vet ./...
go test ./...
go test -race -count=1 ./...
TAG_FILES_MIGRATE_INTEGRATION=1 \
  TAG_FILES_MIGRATE_STORAGE_IMAGE=tag-files-storage:local-check \
  go test -race -count=1 -run TestRealS3 -v ./...
```

The ordinary suite uses independently authored EF-schema SQLite fixtures and
covers all scalar/join/kind fields, raw/tick extrema, WAL-only committed records,
malformed/orphan/missing/processing cases, duplicates, shared media keys, unsafe
keys, source/archive corruption, explicit exceptions, budget stops, lost part
responses, decreasing-capacity interruption/completed replay, corruption refusal
before capacity credit, and interruption after verified objects before atomic SQL
commit. Legacy query regressions use an independent source-table SQL oracle,
cover skipped/token boundaries and precedence, and prove that blocked categories
cannot produce a false complete report. Two lost Create responses across storage
epochs prove that each attempt is journaled before dispatch and no third request
can escape at the same epoch, including after closing/reopening the journal.
Exactly sized decreasing-capacity partial-MPU fixtures cover lost ACK/checksum
proof, native checksum-less ListParts with validated ACKs, ETag-only refusal,
conflicting/noncanonical/unbound parts and a fresh full-reserve gate at Complete.
Actual generated PNG/MP4 and corrupt bytes exercise FFprobe without transcoding
any migration input.

The real race-enabled rehearsal passed: **12 copied objects / 14,000,086 bytes**,
two genuine versions of a Unicode key plus a retained delete marker, all four
category sets, full source/destination hashes, real 5 MiB multipart, lost
successful PUT **and Complete** replies, partial-MPU interruption before Complete,
resume and replay. Native HEAD/ListParts independently observed 14,000,000 stored
partial bytes while constraining signed real capacity to an exact logical
T+W+D+R pool. Resume required verified part credit through durable SDK-validated
ACKs on the checksum-less native ListParts path. This models decreasing logical
payload capacity; it does not measure physical backend working peaks. The source is a
separate pinned SeaweedFS 4.53 fixture behind an enforced GET/HEAD-only gateway;
the target is the production owned-storage image with pairing/epoch/capacity.
The final post-fix run took 29.62 s end to end (23.45 s scan/copy/verify portion),
using target image ID
`sha256:22fb8cc103c992f86d84449312e92cecf5340f83f59e00d529e7c726458e4e1a`.
Fresh `go vet ./...` and `go test -race -count=1 ./...` also passed after both
query-token and multipart epoch fixes and the complete/partial resume-budget fixes.
An earlier attempt stopped at target image inspection because its previous
image ID was no longer installed; the final run above used the current image.
Fixtures use
private disposable volumes/random credentials; containers/volumes were removed.
This is a small synthetic sizing fixture, not throughput or full-library RTO.

The separate [large CLI rehearsal](LARGE-EVIDENCE.md) also passed actual normal
`scan`/SIGKILL `copy`/resumed `copy`/`verify` for a 2 GiB object in 128 real
16 MiB parts. Three durable native/local ACK tuples survived unchanged and no
part was resent; all bytes, metadata and independent catalog/category ID sets
matched. Raw child RSS stayed within the chosen 256 MiB test regression bound.
Run it with `scripts/check-migration-large` from the repository root. Its
point-in-time native disk observations do not establish the physical working peak.

Explicit remaining gates: the owner's actual MinIO version/encryption/lifecycle
and native rollback snapshot; representative continuous storage working peaks and
full-library disk sizing; 100% real source audit and owner exceptions; a full
pilot/offline-window measurement; target media/browser/rename/sort/Range and
release-container acceptance on a disposable copy; physical Mac/VM power-loss.
MinIO registry pulls were denied and its historical public binary URL returned
410 in this environment, so the real rehearsal must not be described as MinIO
vendor compatibility evidence. The tool's S3 version/metadata/tag API path does
have real backend evidence above.
