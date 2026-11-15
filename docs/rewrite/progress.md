# Rewrite implementation progress

Scope remains the full rewrite in [the implementation plan](../superpowers/plans/2026-11-14-production-rewrite.md).
No release, migration of real data, or production-readiness claim has been made.

## Current state (2026-11-15)

- Work remains local on main; original clean baseline was `bc7d62d`. Legacy
  C#/Angular sources and explicit legacy Compose copies remain for reference.
- Tasks 1–3 are implemented: durable SQLite/schema/jobs, search/organization/
  catalog semantics, generated OpenAPI contracts, HTTP commands, navigation
  snapshots and durable replay. Current schema is6, external library format1.
- Tasks 4–5 are implemented and extensively exercised on native Linux arm64:
  owned private SeaweedFS4.53, physical/logical pairing, boot-epoch fencing,
  immutable streaming uploads, asynchronous verified Complete, primary/poster
  jobs, independent cleanup/deletion, same-origin media, bounded scratch,
  physical admission and joined startup/recovery/shutdown.
  **One explicit storage contract discrepancy is being corrected:** backend
  ListParts omits SHA256; inferred binding hashes are insufficient for the
  pre-Complete checksum requirement, even with full post-Complete verification.
- Task 6 is implemented: actual React v27 UI, library/organization/atomic
  editors, uploads and canonical viewer. Latest local evidence:83 web unit
  tests,14 Chromium/WebKit API-browser cases, two actual-image browser uploads
  and retained primaries/deep links after simultaneous pair restart.
- Tasks 7/8 external tools are implemented with independent source/target,
  resumable migration and complete cold native backup/restore. Native S3 and
  actual Docker recovery/install/update rehearsals pass on synthetic fixtures.
  Source-MinIO/full actual library and Mac evidence remain open.
- Task 9 is in progress: root/deploy Compose now run the new app/storage;
  release.published workflows verify before GHCR multiarch publication.
  Both image architectures build; actual amd64 services also passed under
  QEMU, which does not establish native amd64 CI or Mac compatibility.
- Actual20GiB streaming/retained-failure/cleanup passed with bounded normal-app
  RSS. The growth-profile SQL/API/encode/recovery performance probe passes locally.
- [Acceptance matrix](ACCEPTANCE.md) separates F/D implementation evidence from
  open gates. Final review, checksum correction, maximum browser/valid-large-
  media evidence and real Mac/full-library/durability acceptance remain open.

## Decisions

- Use the recommended Go + React stack and accepted v27 UI. The provided
  documents already specify the design; implement under the existing request.
- Keep legacy source/build files until replacement acceptance, so reference
  behavior and the release process remain available during development.
- Work directly in main; no worktree, branch publication or real-data writes.
- Equal values in each of the six deterministic sorts use ID ascending; this
  supplies the stable tie-breaker left unspecified in the legacy behavior.
- Application mutations receive the caller's SQL transaction, so the next HTTP
  layer can commit business changes and operation results atomically.

## Verification evidence

`go test ./internal/store/...` passed against mattn SQLite 1.14.53 and Goose
3.30.1: fresh/reopen, exact nullable/int64 ticks, process lock, all reader
connection pragmas, actual BEGIN IMMEDIATE, rollback, safe IDs/FK, incompatible
format/schema byte-for-byte no-write rejection, ready asset ownership/immutability,
interrupted initial install recovery, immutable part bindings and independent
cleanup, concurrent claims/reclaim/stale fencing/heartbeat/atomic commit/retry.

`go test ./internal/search/...` passed: golden grammar, malformed expressions
and positions, Unicode/.NET character parity, limits, duplicate/case bindings,
unresolved terms, rename/create distinction, AST JSON round-trip and real SQLite
execution of compiled golden predicates. Unicode checks cover the inspected
.NET UTF-16/Letter/DecimalDigit rules, not every Unicode-runtime version pair.

`go test ./internal/organization` passed: duplicate/unresolved category binding,
rename preserving result sets, explicit text rebinding, name-only patch,
optimistic versions, rollback on an invalid stored AST, full category/collection
CRUD, nullable kind, deleting collection without deleting items, member version
bump, 351-entry untruncated directory, oldest-item cover without poster fallback,
global duplicate-name statistics including unused tags and UTF-16 name limits.

`go test ./internal/catalog` passed: all five kinds, six deterministic sorts
with NULL/int64 ticks and ties, 305-item full pagination, reproducible seeded
Random and new random contexts, query/kind/collection intersections, saved AST
filter source, exact nonempty similar sets, atomic optimistic bulk replace,
separate tag/collection APIs, favorite set and view-event deduplication, malformed
UUID rejection, and GET operations with no counter side effects.

`scripts/check` passed with all current modules: generation comparison, gofmt,
vet, ordinary tests and race tests. The offline run passed with cached pinned
Go toolchain/modules and GOPROXY/GOSUMDB disabled.
The environment exports GOROOT for Go 1.27.3; an offline run with the downloaded
1.27.4 binary must unset that inherited GOROOT to avoid mixing toolchains.

Current Task 3 `scripts/check` passed after the HTTP review fixes. HTTP scenarios
validate actual responses against the generated OpenAPI schema: safe public DTOs,
ETag/If-Match, nullable PATCH, full metadata and separate bulk APIs, atomic stale
bulk rollback, saved-search identity through rename, UUID view deduplication,
explicit favorite sets, pagination, Problem Details and bounded JSON bodies.
Malformed trailing JSON/text is rejected before any metadata or receipt write.
The source contract also validates the deferred raw part, media Range/HEAD and
Complete operation-key boundaries; this is schema evidence, not handler evidence.

Real SQLite operation tests passed for lost-response/reopen replay, concurrent
same-key execution once, route/body conflicts, retained explicit failures with
business rollback, unexpected-error full rollback, seven-day expiry (including
writer wait), and pruning without deleting old failed uploads. Navigation tests
passed for persisted order/origin, deleted-ID skipping, 24-hour expiry, saved AST
version snapshot, seeded gallery order, explicit order, 20k bound, duplicate and
unsafe inputs, and a percent-encoded 4096-character Unicode query URL.

Task 4 wire adapter: `scripts/check-storage` passed (39.821s) with race detection
on Linux arm64, SeaweedFS 4.53 index digest from the ADR, S3 SDK 1.114.10/root
1.48.0/Smithy 1.28.9. The tests use disposable private named volumes, persisted
global fsync, temporary loopback test ports and random credentials. They cover
PUT/HEAD/full GET/hash, user metadata/Unicode keys, wrong object/part SHA256,
anonymous and wrong-secret refusal, Range 206/416, ETag/date conditions,
ListV2 pages, deletion replay, storage restart, single/multiple-part Complete,
InvalidPart refusal, Abort, lost part/Complete responses and full reconciliation.
Both ListParts and ListMultipart are exercised beyond their first 1000 entries.
The test containers and volumes were removed; existing libraries were untouched.

Seaweed 4.53 ListParts omits checksums and can return identical retry aliases.
The adapter retains missing SHA values, collapses only equal returned records
and refuses conflicting aliases. Complete requires the prior binding digests
and checks them against every byte from full GET. The profile is documented in
`app/internal/objectstore/README.md`; an ETag never supplies a content hash.
Fault tests prove no automatic resend of a consumed nonseekable body and full
GET/hash after a successful but corrupt Complete response. An isolated transport
benchmark allocated about 38KB for both 8MiB and 64MiB parts; this is adapter
allocation evidence, not a measurement of production HTTP RSS/throughput.
The ordinary full `scripts/check` also passed with the new modules.

The separate `cmd/storage-check` is built and run inside the pinned storage
container, without any application DB or Docker socket access from the app.
It checks the owned LevelDB2 profile and metadata containment in complete /data,
bounded regular config files, strict private identity shape and explicit
Read/Write/List permissions, persisted global fsync without overriding false/
TTL/read-only/WORM rules, anonymous/signed HEAD, access-block settings, bucket
policy/ACL and lifecycle metadata. It performs no repair or data mutation.

After review fixes, `scripts/check` passed again. `scripts/check-storage` with
race detection passed for objectstore (43.744s) and storageprofile (24.204s).
Real negative checks refuse missing/overridden fsync, TTL, disabled public access
block, bucket policy/public ACL, lifecycle expiry, wrong backend and empty
credentials. Restart preserves the accepted profile. A real historical object
ACL allows anonymous GET with the block off, then returns 403 with the block on.
Unit regressions cover readonly identity permissions, unknown identity-policy
fields, owner-only ACL/single XML document, FIFO rejection and the io.Copy fast path
which could otherwise bypass a bounded writer. Fixture resources were removed.
The older wire fixture still creates its bucket before applying fsync. The
production bootstrap has separate actual-image evidence below.

Owned storage bootstrap: `scripts/check-storage-image` passed (138.213s) on
linux/arm64 against the exact built image, with host tests under the race detector.
The image pins Go 1.27.4 and SeaweedFS 4.53 indexes, runs a root supervisor and
unprivileged backend, and locks the entire /data volume until backend reaping.
Durable initializing/ready metadata, secure separate identities and config hashes
survive both SIGKILL and SIGTERM during initialization. Unit regressions cover
second ownership, unknown/unowned state, config-write interruption, changed
credentials, tampering, external lock symlinks, publication after Close and
nonblocking/bounded secret files. Unknown storage and credential mismatch are
also refused by the actual image without rewriting their source bytes/state.

The supervisor initially disables S3, persists and reads back global fsync,
stops/reopens the backend, then provisions the private bucket/public-access block.
All backend ports bind loopback; only after the full live profile passes does a
bounded streaming gateway open network port 8333. A failing real regression
proved the earlier startup window exposed S3 before bucket-access validation.
The fixed image refuses both unsafe fsync and disabled public-access-block
settings without exposing S3. It does not silently heal existing unsafe settings.
The gate preserves SigV4 Host, raw bytes, Range 206/416 and conditional 304.
Tests verify full object hashes, retained confirmed MPU parts after SIGKILL and
ordered completion/full hash through the gateway. Anonymous object access and
creation of another bucket by the scoped app identity are refused.

First physical object allocation/PUT took about 10.7s in this fixture; image tests
allow 60s for S3 calls. This is recorded startup/allocation latency, not an upload
throughput or bounded-RSS result. Both `scripts/check` and `scripts/check-storage`
passed again after the gateway change (40.413s objectstore/23.893s storageprofile).
All disposable fixture containers/volumes were removed. The amd64 image also
cross-built successfully and its extracted supervisor is a static x86-64 ELF.
This Docker host advertises arm64 only; amd64 execution and physical Mac/VM
power-loss evidence remain open. Root Compose and app admission still need the
new storage image; the legacy installation is deliberately still present.

Ingestion metadata foundation: real SQLite tests pass for all-or-nothing batch
creation, duplicate filenames with distinct canonical UUID identities, collection
snapshot, exact UTC timestamp and owned filename-independent incoming keys.
No processing job is created before verified incoming bytes. Invalid later
input and an injected failure of the second SQLite insert leave no partial batch.
The MPU identity can be attached/replayed but never replaced. Parts are bound
before forwarding to exact persisted geometry and canonical base64 SHA-256;
changed digest/size, wrong generation and premature writes are rejected.
Reopen preserves these bindings, and concurrent different-digest registrations
produce exactly one winner. Queue batches are current-session projections:
old failures remain and empty batches disappear. Filename bounds match OpenAPI
Unicode characters without normalization. The full `scripts/check` passes with
this module and generated queries. These are metadata tests; no ingestion S3
forwarding, drain/Complete, cancellation or publication handler is claimed yet.

Storage lifetime fence: fresh `scripts/check-storage-image` passed (138.771s)
after adding owned boot-epoch discovery and expected-epoch request fencing.
The real restart test receives a different boot UUID, rejects the earlier
epoch with S3 `409 StorageEpochChanged`, proves no MPU was created by that
request, then continues the retained MPU with the new epoch and full hash.
Normal S3 clients remain compatible. The SDK's bound client leaves the base
client unchanged. Missing/malformed/duplicate response epochs are rejected;
unit and image regressions caught duplicate headers and proved the single-value
fix. `scripts/check` and real `scripts/check-storage` passed again (40.201s wire,
23.944s profile), and both image architectures cross-build; only arm64 executes
on this host. The fence must be recorded in each intent before forwarding and used
with owned-result reconciliation, not as proof that an old write failed or succeeded.

External-write journal foundation: a new atomic Goose schema-1-to-2 migration
preserves the library UUID, duplicate Unicode tags, IDs, old pending/error sessions
and full int64 ticks. Independent live/uncertain intent rows survive parent deletion
and reopen. Owner/target/generation/key/epoch/arguments are immutable; retries create
new records, terminal history states are refused, and no expiry exists.
The upgrade exposed a preflight gap: a future version in an earlier Goose row
could hide behind the latest known row. A failing existing regression was fixed;
all history entries now participate in read-only compatibility rejection.

`scripts/check` passed after the journal/manager addition: generation, vet,
ordinary tests and race tests. Controlled HTTP-peer tests use the real SDK and
SQLite to prove intent visibility before forwarding, a free writer during S3,
bounded admission and drain through metadata acknowledgement. Unknown responses
remain independent after session deletion; the same epoch cannot trigger cleanup,
and a new epoch does not retire work until the domain reconciliation callback
succeeds. Unknown payload versions remain untouched. Regressions also cover
UUID case equivalence, caller cancellation and a slow metadata acknowledgement
not consuming the later settlement deadline. Explicit epoch refusal is not
archived as uncertain work. This manager is not yet wired to actual ingestion;
native session/part/complete/candidate/cancel/cleanup scenarios remain open.

Typed I/O dispatch now owns the actual Create/UploadPart/Complete/Put calls.
Callbacks cannot replace dispatch or rebind the read/reconciliation interface.
`Put` propagates a lost response without looking at an older matching object;
that intent stays uncertain. Successful Put requires full GET/hash before its
metadata callback; corrupt returned bytes never reach it. UploadPart forwards
the committed tuple/checksum and waits for a verified acknowledgement. Complete
checks the versioned manifest identity and copies the caller's mutable slice,
then requires the adapter's full incoming verification. Controlled-peer tests
prove these boundaries with real SDK/SQLite, and fresh `scripts/check` passes
including race checks. Actual native ingestion wiring is still next.

Ingestion coordinator now joins durable sessions with typed I/O dispatch.
Create inventories only the exact owned incoming key; a lost response adopts
that MPU without repeating Create or discarding its uncertain intent. Empty
listings do not settle unknown writers. Concurrent initialization dispatches
only one Create. Part binding and write intent commit together before the
nonseekable stream; acknowledgement updates are generation/state fenced, and
the SQL writer remains free during storage I/O. Refresh reconciles ListParts
only against prior bindings, rejects unbound/size/checksum mismatches and resets
missing acknowledgements without changing byte identity.

Complete closes SQL admission before draining known writers through their
acknowledgement, reconciles parts, and freezes a versioned ordered manifest
before forwarding. Missing parts reopen admission with bindings intact. Each
successful response requires HEAD and full GET/part/full-file hashes before the
atomic source-hash/queued/job transaction. Retry of a frozen manifest verifies
the same incoming bytes; InvalidPart cannot rebuild it from another listing.
Tests cover lost replies, corrupt 200 responses, concurrent admission, failed
job insertion rollback, generation changes and parent removal during full GET.
Matching read recovery can queue work but retains uncertain writers independently.

Fresh `scripts/check` passes (generation/gofmt/vet/ordinary/race); additional
concurrent-Create race coverage passes. The actual arm64 production image test
passes under race detection (32.593s, 31.58s scenario): lost Create and part
responses, exact MPU adoption, rejected wrong digest/body, ListParts recovery
without SHA, storage SIGKILL/restart with retained parts and new boot epoch,
lost Complete response recovered by full source hash, SQLite close/reopen and
stable queued job identity. Test containers/volumes are removed. This is the
coordinator layer, not HTTP/browser or media publication acceptance. Multiple
owned MPUs currently return a preserved conflict pending cleanup reconciliation;
uncertain-intent recovery after a barrier, cancel/ready/cleanup, startup pairing,
and HTTP wiring remain unfinished. No age-based retirement was introduced.

Cancellation/cleanup slice: Cancel is a SQL command that first resolves an
already-ready item, then atomically snapshots owned incoming/MPU/unpublished
candidate references into an independent versioned receipt/job and removes
session/parts/ingest jobs. Repeated cancel returns the same pending cleanup;
unknown or fully cleaned uploads stay absent. Real SQLite tests prove complete
rollback if the receipt cannot persist, stable identity after reopen, no visible
empty batch/history, retained independent write intents and permanent stale
worker rejection after the ingest job is removed.

Claimed cleanup checks permanent ownership/fences, drains known writes/read
callbacks, aborts only exact-key owned MPUs, deletes only referenced incoming/
candidates and verifies absence. Foreign prefix neighbours remain untouched.
An uncertain writer at the same epoch keeps the receipt/job even after repeated
empty scans and removal of late objects/MPUs. A new epoch requires another owned
result reconciliation before intent deletion. The final receipt/asset/job removal
is one leased transaction; a late SQL failure rolls all metadata back for retry.
Unknown versions and unowned payloads are preserved. No session age is used.

A failing regression exposed that read-only verification callbacks were absent
from the drain registry: cleanup could delete their source mid-read. Reads now
stay registered until their bodies are consumed/closed, and all-operation drain
waits them; the regression passes. Other tests cover cancel during live Complete,
late fully verified result rejection and separately repeated cleanup.
Fresh `scripts/check` passes including generation/vet/tests/race. Both current
owned-image ingestion scenarios pass under race (66.010s total; 33.21s upload/
restart/queue and 31.77s cancellation). Native cancellation waits an accepted
Complete, retains its lost-response intent, removes incoming while keeping the
receipt, survives SQLite reopen, and retires work only after storage SIGKILL/
restart plus exact result reconciliation. All fixture containers/volumes are gone.
This does not yet include active media-process cancellation, scratch cleanup,
ready publication races, startup persistent pairing, active-session uncertain
recovery, duplicate MPU adoption, the job runner or HTTP/API wiring.

Active-session recovery now inventories exact owned keys, preserves an attached
MPU, and validates each old Create/part/Complete against durable identity and
byte bindings before retirement after a storage epoch barrier. A previously
empty unknown Create may then initiate one fresh MPU; a matching incoming file
requires the frozen manifest/full GET/hash before queuing. If Complete had not
created the object, only identical persisted selectors may be sent again. A
changed ETag/hash, unbound part, incompatible payload or future generation blocks
that work. Same-epoch unknown writers remain durable while the method can return
the current usable pending/queued result alongside `ErrUnsettled`.

For several exact-key MPUs without a prior binding, the coordinator chooses the
lexically first opaque ID and atomically stores independent extra-MPU receipts
and jobs. An attached MPU always wins, regardless of listing order. Repeated
inventory deduplicates receipts; an injected second receipt failure rolls back
the entire selection. Claimed extra cleanup checks the stable binding, aborts
only the recorded loser and verifies its absence; it never deletes incoming or
aborts the selected/foreign MPU. It can outlive removal of the session.

Three regressions were reproduced and fixed: epoch change bypassing a known
request still finishing its metadata acknowledgement; a frozen session with an
old part intent stopping at HEAD404 before the first Complete; and adoption/
cleanup scheduling before rejecting an unsupported intent version. Recovery now
waits and reloads known work, accepts only the original frozen selectors when
no incoming object exists, and decodes all paged source intents before inventory.
Fresh `scripts/check` passes including generation/vet/ordinary/race tests.
Both real owned-image ingestion scenarios pass under race for the final code
(64.907s total; 32.08s active recovery and 31.80s cancellation). After SIGKILL,
native active recovery retires old Create/part intents, retains the chosen MPU,
records and aborts an extra owned MPU while preserving a foreign neighbour, then
continues lost-Complete/full-hash queuing and SQLite reopen. Fixture resources
are removed; the production storage image code itself did not change.
Publication, active media processes/scratch, persistent startup pairing,
job-runner/HTTP integration and the remaining rewrite tasks are still open.

Primary publication slice: schema 3 atomically upgrades existing schema 1/2
libraries and adds nullable asset media_kind/duration_ticks. Old candidates keep
their keys/data and receive no invented descriptor. New output descriptors
(content type, dimensions, rotation, kind and exact int64 ticks) are immutable
along with the prior size/hash/key invariants. The factory accepts only validated
image/video output facts from a future media worker, persists a fresh candidate
ID/key for every allocation/lease, and checks the current source/job generation.
These descriptor checks do not replace decoding/probing the media itself.

Primary upload first fully verifies an existing candidate; a mismatching object
is not overwritten. A new PUT uses the durable expected size/SHA and independent
intent, then full GET/hash before the fenced verified mark. A lost response may
recover through a full read without another PUT, while its uncertainty remains.
Ready publication atomically creates the item/primary link, advances library
generation, removes the session/parts/ingest job, and creates source/unused-
candidate cleanup and separate versioned poster work. Uploaded names are not
retained. Complete and repeat publication resolve the existing ready item.

Fault tests cover failure of cleanup/poster insertion rolling the whole ready
transaction back, actual concurrent ready/cancel, stale leases despite clock
rollback, and a delayed old PUT after a newer lease publishes different bytes.
Old keys cannot replace the new primary; independent ready cleanup removes those
unused bytes while preserving the published asset. Separate primary-intent
reconciliation survives session removal and requires a new epoch/full expected
hash for a published object. Exact maximum int64 video ticks come from the durable
candidate descriptor, even if the caller later changes its original memory.
Regressions fixed pending processing outcomes masking a failed ready commit and
uppercase UUID spellings being validated without using their canonical value.
The documented default upload concurrency is now two, matching the product spec.

Fresh `scripts/check` passes after these changes (generation/gofmt/vet/ordinary/
race). A real PNG native scenario passed: lost candidate PUT response, read-only
recovery, collection-preserving ready, independent incoming cleanup that retains
primary, pending poster work, storage SIGKILL/restart, primary intent retirement
after full hash and SQLite reopen/ready replay (32.986s under race). The final
combined native suite passes all three scenarios under race (96.411s total:
32.00s active upload recovery, 31.73s cancellation, 31.66s primary/ready/cleanup).
Fixture containers/volumes are removed. The storage image itself is unchanged.
The final clock adjustment includes preliminary ready-lookup time in the leased
transaction deadline. Fresh `scripts/check` passes again, and the affected native
primary/ready/cleanup/restart case passes again (32.550s under race).
No real media worker, FFmpeg output validation, generated poster, process/scratch
cleanup, disk admission, media/system/upload HTTP routes or runnable SPA/app image
is claimed here; Tasks 5–9 and final startup/pairing gates remain unfinished.

Media processor foundation: installed/tested FFmpeg `7:8.0.1-3ubuntu2` on this
Linux arm64 host and pinned Ubuntu 26.04's image index plus the same FFmpeg package
in the app runtime foundation. The image currently exposes only media-runtime/
media-check stages; it is not a runnable rewritten app. `domain.MediaOutput`
shares the durable output descriptor rules with ingestion without a media/store
dependency cycle. Existing publication checks still pass after the extraction.

The Linux supervisor runs direct argv in a separate process group, bounds stdout
and stderr, cancels with TERM/grace/KILL, always waits, and reaps owned adopted
descendants. Real subprocess tests cover literal shell-like arguments, output
flood/failure status, TERM-ignoring descendants, and children surviving successful
parent exit both with and without inherited pipes. Subreaping is process-wide;
Wait4 only targets the owned group after its leader has been reaped.

Probe uses local regular files, explicit protocol/demuxer/decoder allowlists,
bounded metadata/probe time/streams/decoder allocation/pixels and byte-derived
type. Audio-only/cover art and unsupported FFV1 do not become video. Prepare fully
decodes supported input/output, keeps usable photo bytes, converts other supported
photos to PNG, and emits MP4 H264/AAC veryfast CRF23/128k/faststart without resizing.
Real tests prove MPEG4/PCM conversion, silent rotated video with cleared output
rotation, PNG identity despite misleading extension, BMP conversion, truncation,
input preservation and removal of failed output, pixel/byte caps, and zero HTTP
fetches for an external playlist. A fixture bug was characterized with ffprobe:
the old rotate tag produced no display matrix in this build; the fixture now
uses display_rotation and verifies real input rotation before testing conversion.

Ticks use bounded fixed decimal/integer-rational parsing and exact 100ns rounding;
unknown stays NULL, negatives/overflow/exponent syntax fail. Context-aware local
copy/hash stops before reading more data after cancellation. An exact-byte-limit
photo initially failed; the reproduced boundary is now inclusive while the FFmpeg
stop is one byte beyond it and final output size remains enforced.

Fresh `scripts/check`, `scripts/check-media` (host race), and
`scripts/check-media-image` pass. The final image is native arm64
`sha256:ffed523c6ef349c5f9a97a51865a349463312e62e5f0ab25044da990c250e1c2`;
the package query confirms `ffmpeg=7:8.0.1-3ubuntu2`. Image tests execute ordinary
Go test binaries as UID/GID10001 with read-only root, no external network and a
bounded disposable scratch tmpfs; host tests supply the race detector. No fixture
containers remain. These small fixtures do not prove codec RSS, Mac playback or
the full matrix: JPEG/EXIF, animated formats, AVIF/HEIC including tile groups,
aspect/VFR, HDR/color and >4K remain explicit gates. Current odd video/HDR profile
rejection is documented; it is not a completed media compatibility claim.
Disk reservation/507, durable scratch/process recovery, one job runner, poster,
media/upload/system HTTP routes, startup pairing and Tasks 6–9 are still open.

### Whole-photo, animation and orientation regression evidence

The generated HEIC corpus reproduced a real loss of pixels: selecting stream 0
published a 64x64 tile from a 128x96 primary. A 24-tile image also exceeded the
old 16-stream cap. Probe now selects the primary group, resolves group-relative
stream indices, validates coded/visible canvas and full nonoverlapping coverage,
and applies the pixel bound to the whole canvas. Conversion assembles all tiles
before exact cropping; AVIF preserves original bytes after every selected tile
decodes. Native tests check all four source colors, not only dimensions. A damaged
last AVIF tile is rejected even though the first tile and metadata remain usable.

FFprobe 8 does not expose a group's coded transform side data. A bounded local
ISO metadata reader (4 MiB, 4096 boxes) resolves the selected item's associations,
including wide IDs and property indices, and preserves transform order. Actual
grid primary rotation and both mirror axes pass pixel assertions. An initially
incorrect mirror-axis expectation was corrected from pinned FFmpeg/libheif source
and an independent libheif decode: imir=0 exchanges top/bottom, imir=1 left/right.
Synthetic fixtures, generator, provenance and byte hashes are checked in; runtime
tests embed them. libheif encoders/decoder were installed on the host only for
fixture generation/cross-checking, with no runtime image dependency or user media.

JPEG EXIF orientation is frame metadata, absent from the original stream probe.
All eight variants now pass expected displayed dimensions and corner colors;
normal JPEG remains byte-identical, other orientations normalize to PNG with
rotation/mirroring baked into pixels. A second regression showed APNG becoming
one static PNG frame. Animated PNG now retains image/png bytes after full decode,
and native GIF/APNG fixtures verify animation-preserving identity.

HEIC alpha and HDR fixtures reproduced silent transparency/color loss. Those
unimplemented conversion profiles now return unsupported_codec with retained
input and no output; ICC conversion, clean-aperture/scaling and overlapping
overlays remain unaccepted. This is an explicit current profile, not evidence
that the broad camera/color matrix or the complete rewrite has been accepted.

Fresh scripts/check (generation, formatting, vet, ordinary/race suites),
scripts/check-media (host native/race), and scripts/check-media-image all passed.
The exact new native arm64 media-check image is
`sha256:2620ba09e88dde497c19bf8da0064971d2d3c469d829a928e027c0714be7b043`;
both test binaries ran nonroot with read-only root/no external network/bounded
scratch and their containers were removed. A three-second metadata fuzz run
completed 54,426 inputs with two workers without a failure. Checked-in corpus
hashes passed verification. No runnable rewritten server/SPA image exists yet.

Worker/scratch recovery and disk admission, poster/media/deletion runtime,
persistent startup pairing, wider aspect/VFR/color/>4K/browser checks, UI,
offline migration/restore and final release integration remain open.

### Serial worker, scratch and actual ingestion execution

The new runner claims only registered job kinds and runs one handler at a time,
with durable lease heartbeat and fenced completion/failure. Real SQLite tests
prove the lease remains live while execution exceeds its initial duration,
duplicate Run/RunOne admission is refused, unregistered posters remain queued,
and shutdown waits for a cancelled producer before releasing the job. Interrupted
execution does not consume a retry and increments the generation permanently.

Session and ingestion-job error/retry state now change atomically. An injected
session update failure rolls back both; cancelled jobs cannot resurrect an upload.
Manual retry preserves immutable input/MPU facts and resets attempts while fencing
the old generation, even under clock rollback. A reproduced future-payload retry
bug was fixed: unsupported job payloads remain unchanged. Initial execution may
have three automatic retries at 5/30/120 s; the fourth failure stays visible. This
is the recorded interpretation of the three-delay/three-attempt ambiguity.
Independent cleanup repeats with a 120 s cap rather than abandoning uncertain work.

Scratch uses job/generation/attempt UUID paths and a held OS ownership lock.
Admission reserves source plus the entire configured output maximum, accounts
for existing caches/live files/unwritten reservations and uses real Linux
available bytes/inodes plus a physical free-space reserve. Tests prove stale
attempt writes/cleanup cannot affect a newer output, retained caches are counted,
symlinks/path traversal are refused, and live ownership is not released early.
No age-based scratch deletion was added.

The pipeline streams incoming through the owned read manager with a 32 KiB copy
buffer, verifies exact length/full SHA256, syncs/closes the cache, probes/converts
outside SQL, and uses the existing immutable candidate/verified/ready transaction.
S3 input survives until ready and independent cleanup. Native controlled-peer
tests cover a real PNG, corrupted source hash before any candidate/PUT, capacity
refusal before source GET, cancellation of a blocked body and quota exhaustion
during a read. The capacity monitor cancels and joins known reads/processes before
removing only its private attempt; unrelated retained caches remain intact.

The actual owned arm64 storage image plus real SDK/SQLite/FFmpeg passes
`TestIngestionWorkerOwnedImageProcessesPhotoAndRetainsFailedInput` under race:
MPU/complete -> serial worker -> ready PNG/full stored hash, independent source
cleanup, invalid-media error without automatic retry, retained failed source/full
hash, and ready/failed state after library reopen. The final scenario passed in
21.80s (package22.820s). It uses only disposable containers/volumes; no user data.

Fresh scripts/check, expanded scripts/check-media host native/race and
scripts/check-media-image passed. The latter now runs a scratch test binary too;
final arm64 media-check image is
`sha256:c37c598262fc5352a80e319ac1201e731cee351f906697a4dfae6ca455ea46f9`.
Container tests run nonroot, read-only root/no external network/bounded tmpfs;
the CGO SQLite worker is tested on the host and with actual S3, not inside this
disposable test image. No fixture containers remain. A repeated parallel run caught a test-only stale
free-space assumption: cleanup can increase free bytes between statfs samples.
A controlled 16 MiB cleanup reproduced that change; the corrected reserve test
uses an impossible int64 capacity requirement and the final full checks pass.
The ownership lock also refuses symlinks before opening/creating a target.

This is not finished recovery or HTTP507: startup must reset dead-process jobs
independently of wall-clock rollback, prove producer quiescence before reclaiming
old scratch, and validate persistent library/storage pairing before admission.
Full metadata/storage/scratch upload budgets and HTTP mapping, poster/deletion/
media routes, the wider media matrix, UI, external tools and release integration
remain open. No runnable rewritten server/SPA or full rewrite acceptance claim.

### Persistent pair and startup recovery

SQLite schema4 adds a nullable logical storage UUID/physical-root fingerprint
pair, set atomically and immutable once bound. Owned storage format2 records the
same root fingerprint and one library UUID. The format1 upgrade preserves its
logical identity, credentials/configuration and data. Schema3->4 upgrade tests
preserve library/item/tag identities, duplicate Unicode names and max int64 ticks;
old runtimes refuse newer library/storage versions before use.

Storage commits/syncs the pair first; SQLite then uses a conditional write.
Actual-file/real-SQLite tests cover concurrent one-winner binding, restart,
lost storage reply, failed SQLite commit, retry of the same pair, immutable
binding, foreign library/volume refusal and a mixed bound-DB/unbound-storage
restore marker. A physical-root fingerprint (Linux filesystem type/fsid/inode)
prevents a copied marker from claiming an independently live root with the same
logical UUID. The external cold restore/rehearsal tool must validate its frozen
generation, then explicitly rebase this fingerprint in both restored sides;
that tool remains pending. Physical Mac/VM lifecycle is not inferred from Linux
container restart, and pairing is not a coherent-backup/hash validator.

Private GET/POST control uses bounded strict JSON, HMAC-SHA256 request/response
proofs and fresh UUID nonces. Tampering, replay, wrong credentials, oversized
responses and unknown versions are rejected. Library-bound SDK clients retain
all permanent guards across epoch binding without mutating their base client.
Their epoch discovery authenticates persistent identity too: a foreign volume
or older gateway that ignores new headers cannot become a false barrier. S3
guards reject mismatched/duplicate identity headers before forwarding a body.
This is a private storage protocol, not an application backup/diagnostics API.

Startup SQL recovery is one-shot and serialized against the first job claim.
It requires a stored pair and preflights all independent write codecs and known
ingest/cleanup/poster jobs/manifests before commit. Unknown versions and state
disagreements roll back every change. Running jobs receive higher generations
without charging unfinished attempts, even under clock rollback. Queued jobs
become available; retry_wait is bounded by the shared 5/30/120 s policy. Failed work,
inputs, MPUs, candidates, receipts and journals remain. Tests cover 107 pending
jobs across pages and independent poster recovery without recreating uploads.
This SQL reset does not prove an old local process or remote writer stopped.

Fresh scripts/check, expanded host scripts/check-media native/race, and the
nonroot/read-only/network-disabled media/process/scratch image suite passed.
Full scripts/check-storage-image with real media enabled passed under race in
303.844s: real lost-response upload/cancel/publication recovery, negative private/
fsync/credential checks, killed initialization, actual same-volume restart,
foreign-epoch refusal with retained uncertainty, and real worker/photo/failed-
input retention. The owned-pair native case passed 42.84s and worker 20.93s. An
initial native assertion incorrectly expected an XML error code from HeadBucket;
the SDK exposes generic 409 for HEAD, so refusal/journal assertions were corrected.
Bound epoch discovery now uses authenticated control identity instead.

Exact native arm64 images: storage
`sha256:5ff5629e0f28f5746c9504b34a5c020edd9bc548ccc2e39d5aad0b1e9cfb1867`,
media-check
`sha256:0585a94c21dff0ac0c40fbd20cdef55e1b1379b51365e9f634865e47109a3e89`.
Fixture containers/volumes were removed. No production data, push or publication.

Complete application startup/admission/HTTP/shutdown wiring is still absent.
Safe old-scratch producer quiescence/reclamation, full disk507, actual poster/
deletion/media routes, wider media/Mac gates, UI, external migration/cold restore
and release integration remain open. No Task4/5 or full rewrite completion claim.

## Independent review

The requesting-code-review skill was used for read-only foundation/organization
and catalog reviews. Reproduced findings were fixed with failing-then-passing
regressions:

- Future user_version could bypass preflight through an empty/version-zero DB.
- Rendering a flat query added enough parentheses to exceed parser nesting.
- Close released ownership while a Write transaction could still commit.
- A lease could expire while waiting for the writer or inside publication.
- A UUID with extra separator pairs passed validation.

The fixes drain active writers before releasing ownership, reject unknown
versions before initialization, render by precedence, check elapsed lease time
inside the transaction and again before deletion, and require 16 UUID bytes.

The reviewers did not re-review these fixes; regression tests and the full suite
are the fix evidence. No outstanding findings from these two scoped reviews.

A separate HTTP reviewer found trailing JSON accepted before mutations, missing
Complete operation-key fields in the full contract, and 422 instead of 400 for
mixed navigation filter sources. Each was reproduced in a failing regression,
then corrected and verified by the focused tests and full `scripts/check`.
The reviewer did not re-review the fixes. No outstanding findings from that
scoped review. Further inspection also fixed the Unicode origin URL bound and
receipt expiry sampled before waiting for the serialized writer.

The same reviewer performed a separate read-only review of the objectstore
slice and found no concrete defects. It independently passed the real S3 suite,
vet/race tests and additional 1001-entry pagination probes. Those pagination
cases now also have committed integration coverage. This review explicitly
defers SQLite intents/bindings, draining/fences/cancel/crash recovery, production
preflight, HTTP memory/admission, ENOSPC, tools and physical durability.

A further scoped checker review found three Important false positives: promoted
bytes.Buffer.ReadFrom bypassed the output bound, bucket HEAD did not establish
object privacy, and read/list-only credentials passed. All were reproduced with
failing regressions and fixed. Named buffer storage preserves the cap; readonly
S3 metadata checks require public-access block, private ACL/no policy; strict
owned action configuration requires write/cleanup permissions. Additional
inspection covered FIFO blocking, per-object public ACLs and S3 lifecycle rules.
The reviewer did not re-review the fixes; focused and full checks are evidence.
The later owned-image tests establish bootstrap/configuration provenance and the
network startup gate. Final Compose mounts and application admission remain open;
physical flush is not proved here.

Deferred review gates map to the still-open tasks: remaining upload/system/media
handlers and API parity, real S3/media/poster publication, browser view lifecycle,
event retention/performance, external tools/releases, and physical Mac durability.
Manual arbitrary SQL corruption/repair is outside the application contract.

## Guarded crash scratch verification

`scripts/check`, `scripts/check-media` and `scripts/check-media-image` passed
after adding inherited root/attempt flocks, process-start retirement and Linux
parent-death signals. Actual process tests first reproduced a direct producer
surviving application SIGKILL and removal of a still-live attempt; both now pass.
The race detector exposed Start/descriptor-close overlap; explicit lifetime
admission now serializes these operations and refuses retired contexts.

The native host/race and nonroot, read-only, network-isolated arm64 image both
test actual FFmpeg writing output while retaining both descriptors, then kill
its application and prove producer reaping and scratch reclamation. An adversarial
surviving descendant keeps the root unavailable until it stops. Reclaim refuses
live/locked attempts, preflights all entries before mutation, preserves symlink
targets and unproven legacy caches, and removes only known quiescent attempts.
The final tested media image is
`sha256:630328fd76730152b960dea09ba166f5b6fe9d27948b3dae5bc97339fdc6f290`.

The executable startup must still invoke reclaim before admitting jobs/HTTP.
The supported pinned tool profile retains inherited descriptors; arbitrary
commands closing their guards are outside this protocol. Full disk admission,
poster execution, wider media acceptance and Tasks 6–9 remain open.

## Poster byte-profile verification

`PreparePoster` now generates the internal `poster-v1` JPEG profile: 640px
maximum edge without enlargement, preserved display aspect/orientation, white
alpha compositing and a representative video frame at 10% of probed duration.
The first animation frame supplies a static preview. Output is exclusively
created, time/byte bounded, probed/full-decoded and SHA-256 hashed; error/cancel
does not change the primary or overwrite an existing output.

Fresh `scripts/check`, `scripts/check-media` and `scripts/check-media-image`
passed. Actual fixtures cover portrait/landscape/odd/small dimensions, full and
partial alpha, distinct video/animation frames, rotated video, all eight JPEG
EXIF transforms, complete HEIC/AVIF grids and primary transforms, byte exhaustion
and cancellation. The final tested native arm64 image is
`sha256:b05f85c4f7555d7edf13c345630ad77236cbc73f6dfa9fecdabf3ed85fbf22db`.
This section records local media-generation evidence. The following durable
poster slice adds candidate/publication/cleanup and its separate fault/restart
verification.

## Durable poster verification

Independent poster work now streams and verifies the ready primary, uses guarded
scratch and the media profile, and persists unique candidate bytes plus exact
item/source/job/generation identity and independent cleanup before PUT. It works
for legacy items with no upload ID. Publication requires full GET/hash and the
current lease; asset attachment, item version/ETag change, job removal and its
cleanup removal commit atomically. Other candidates keep their own receipts.
Poster errors retain primary/item and never recreate a visible upload session.

Cleanup uses a short SQL snapshot to prove permanent retirement, drains known
I/O and preflights all journal pages before deleting only the exact owned key.
An expired lease alone grants no permission. Unknown writes and future codecs
retain attribution. Published unknown PUTs require an authenticated same-volume
epoch change and full hash; orphan writes additionally reconcile deletion.
Startup validates pending receipts before resetting interrupted generations.

Fresh `scripts/check`, expanded `scripts/check-media` and
`scripts/check-media-image` passed. Fault tests cover lost PUT/replay without
rewrite, corrupt/mismatching objects, stale-generation winner, SQL rollback,
tombstone/removed-job late publication, legacy ownership without sessions,
future receipt and future journal entry beyond page 1, source integrity,
capacity cancellation and producer joining. Shared bounded source streaming,
scratch lifecycle and media failure classification serve ingestion and poster.

`TAG_FILES_MEDIA_INTEGRATION=1 scripts/check-storage-image` passed in 346.047s
under race detection against the actual owned native arm64 image. All prior
upload/cancel/ready/storage-profile/pairing scenarios remained green. New poster
work proves real lost PUT recovery, published/orphan journal retention at the
same epoch, SQL reopen and storage SIGKILL/restart, and resume from durable
primary after the previous local input/output disappear. The new attempt uses
a fresh candidate and independent cleanup removes the interrupted candidate.
The end-to-end ingestion runner also publishes and fully verifies its poster.

Tested images: storage
`sha256:f644172904edaf6432cc9ec7c572bf84383e40410b400e0ad08748e7da379fa4`,
media check
`sha256:9be3b2e301e6054478e8b8d4860572c068b98ca50d9db4f0506ede225364f1ac`.
One earlier focused fixture failed because its proxy retained Docker's previous
ephemeral port after restart; updating the fixture target restored authenticated
control. The final full suite passes and disposable containers were removed.
The following deletion slice supplies separate evidence. Executable app/startup,
whole-volume admission/507, media/upload HTTP, wider media/browser acceptance and
Tasks 6–9 remain open. This evidence does not establish a runnable rewritten release.

## Item deletion verification

Schema 5 adds a normalized, immutable, paged deletion inventory independent of
live item/asset rows. The versioned DELETE route atomically hides an item, fences
poster work, snapshots attached primary/poster keys plus the known incoming key,
and creates a repeatable job/receipt. Legacy Unknown/Text/Audio items retain this
contract; no UI action, trash or Undo was added. Pending repeats reuse the same
202/job identity, including after metadata removal; verified repeats return 204.

Deletion waits for independent source/orphan cleanup, preflights all inventory
and write-journal pages, drains known I/O and reconciles unknown writers only
after the authenticated same-volume epoch barrier. Incoming handling aborts
only MPUs for its exact key, then repeats HEAD/Delete. A per-resource checkpoint
commits before DELETE/Abort; lease/CAS checks prevent stale checkpoint downgrade.
Metadata can disappear while minimal keys/receipt/job remain until a later
storage lifetime confirms absence. Reappearing objects are removed again and
retain a new verification obligation. No working state expires by age.

Fresh `scripts/check`, expanded `scripts/check-media` and
`scripts/check-media-image` passed. Tests cover version/HTTP replay, atomic SQL
rollback and poster cancellation, future jobs/codecs, foreign inventory and future
journal entries beyond the first page, writer independence, lost DELETE replies,
resurrection, stale epoch CAS, dependent poster cleanup and owned/neighbor MPUs.
Real schema-4-to-5 upgrade preserves the bound storage pair, library/legacy IDs,
duplicate Unicode tags and maximum int64 ticks. Startup refuses future deletion
payloads atomically, then fences/resumes known work while retaining keys.

The first review exposed missing incoming ownership after the earlier ready
receipt finished. A failing regression drove the explicit incoming inventory,
NULL unknown source byte facts and exact MPU cleanup; the final checks include
this correction. Earlier passing checks without it are superseded.

`TAG_FILES_MEDIA_INTEGRATION=1 scripts/check-storage-image` passed in 349.274s
under race detection against native owned arm64 storage. The real HTTP/worker
scenario uploads and verifies primary/poster, deletes through the actual route,
retains the restart obligation, closes/reopens SQLite, kills/restarts storage,
and confirms primary/poster absence before completing deletion. A stale Complete
does not recreate the item; unrelated failed input remains fully verified.
All previous storage/upload/cancel/pairing/poster scenarios also pass.

Final tested images: storage
`sha256:735f9fbee92ad3462368ec3cec0d6212af3ccff43dff4bd77006c4ecd54ae126`,
media check
`sha256:4c93102f9f0805efad59579b877a266be8bfea8f93966acf1fb4e30faf32775d`.
Disposable containers were removed. These tests prove the named Linux process
restart scenarios, not whole-VM/Mac power-loss durability or final release readiness.

## Media HTTP delivery (2026-11-15)

Owned media now bypasses JSON binding and streams bounded GET/HEAD responses,
single/multipart byte ranges and RFC conditional requests. HEAD ignores Range.
Each GET pins the selected opaque ETag and validates size/range. Candidate,
foreign and deleting resources cannot be read; deletion waits admitted reads
of that exact key. Legacy Unknown/Text/Audio and active/unsupported MIME types
use safe attachment responses without changing their stored bytes or metadata.
Missing primary becomes unavailable; missing poster keeps primary ready and
queues independent regeneration. Reads do not count views. A later upstream
failure aborts the binary response, rather than appending Problem JSON.

Fresh `scripts/check`, expanded `scripts/check-media`,
`scripts/check-media-image`, and
`TAG_FILES_MEDIA_INTEGRATION=1 scripts/check-storage-image` all passed. The full
owned native arm64/race suite took 334.896 s; its worker case exercises actual
SeaweedFS media requests, missing-poster regeneration, HTTP deletion and storage
restart verification. A separate focused run passed in 33.261 s. Controlled SDK
peer tests cover multipart bytes/length, conditions, missing/wrong/truncated
upstream, actual TCP cancellation and concurrent deletion. Range fuzzing passed
73,985 executions in 5 s, including integer overflow seeds.

An 8/64 MiB real HTTP/SDK-peer allocation benchmark used 320,208/316,336 B/op
respectively; this is bounded-allocation evidence for that fixture, not measured
production throughput or process RSS. Logs:
`/tmp/tag-files-media-http-{check,media,media-image,storage-image,fuzz,bench}.log`.
Final tested native images: storage
`sha256:81115aaf5695563b4635db9838d44492ec829cfe7bd176af014856610a2a221b`,
media check
`sha256:cdd48a2916ba955496166415233c288372724ff09bf0b063ced2fd5aa6f9423f`.
All check sessions exited 0 and disposable containers were removed.
Application startup/admission, full disk 507, upload/system HTTP, the remaining
media/browser/Mac matrix, React v27, offline tools and runnable release containers
remain open. Task 5 and the full goal are not complete.

## Application startup and joined shutdown (2026-11-15)

`application.Open` now holds the library/scratch owners, rejects overlapping
canonical roots, verifies/binds the persistent storage pair, preflights and
recovers SQL, then reclaims guarded scratch before HTTP or worker admission.
It constructs one runner with all four durable handlers. Future payloads,
foreign pairs and live scratch producers refuse startup without removing their
working data. A relative/absolute path-alias regression was reproduced and fixed.

The development `cmd/tag-files` server now runs implemented APIs with explicit
secret files/configuration, readiness/liveness/system status and actual
SIGINT/SIGTERM shutdown. Admission closes before cancellation. HTTP connections,
admitted handlers and the worker are joined before scratch/SQLite close;
forced connection close does not retire a still-running handler. Interrupted
poster reading requeues with a newer generation and no retry charge, preserves
primary and leaves no live attempt. Unmeasured S3 capacity is explicitly unknown.

Fresh `scripts/check` and expanded `scripts/check-media` passed including that
last worker-shutdown regression; `scripts/check-media-image` passed against its
unchanged media/process/scratch sources. Full
`TAG_FILES_MEDIA_INTEGRATION=1 scripts/check-storage-image` passed in 361.191 s
(13 cases, race). Its new application case took 25.83 s: actual interrupted
ingest recovery, FFmpeg, primary/poster HTTP, paused/resumed SeaweedFS readiness,
built server executable, health subcommand, actual SIGTERM and owner reacquisition.
The final native source remained unchanged while the additional ordinary
application test was added and the ordinary/host suites were rerun.

Tested native arm64 images: storage
`sha256:26890d637aa4516ef529db46f5c3d93180ddb5a619ff015c32bd78b6f7e4f134`,
media check
`sha256:0da229c8112f0598d19a1c60af7cac4e0627737749f022a5179425f97f7dab2b`.
Logs: `/tmp/tag-files-runtime-final-{check,media,media-image,storage-image}.log`
and `/tmp/tag-files-runtime-worker-shutdown.log`. All check sessions exited 0;
disposable containers were removed. This supplies a development server, not the
completed app/SPA release image. Upload HTTP/full disk admission, the remaining
media/browser/Mac matrix, React v27, offline migration/backup/restore, Compose/
release publication and final branch review remain required.

## Physical disk admission and 507 (2026-11-15)

The owned storage now supplies authenticated capacity for its exact bound root;
the observation and unchanged format2 root fingerprint use one opened descriptor.
An unknown/replaced root refuses measurement. Runtime budget reads durable demand
before observations, combines shared filesystems, accounts pending MPU/incoming,
one media working set, poster, scratch, uncertain/orphan writes and metadata
headroom. Reopening keeps reservations. Sparse length and unrelated active-cache
files no longer create free-space credit; both regressions were reproduced and fixed.

The factory gates coordinator writes and media attempts; metadata errors map to
507 without consuming operation keys or blocking prior replay/deletion. Status
reports measured/unknown capacities. Batch/retry has an atomic budget boundary
tested with concurrent 500-file creation; its actual HTTP adapters remain open.
The configured reserve defaults to 1 GiB/1024 inodes, with guest/host limits still
subject to the external deployment checks.

Fresh `scripts/check`, expanded `scripts/check-media`, `scripts/check-media-image`
and full `TAG_FILES_MEDIA_INTEGRATION=1 scripts/check-storage-image` passed. The
full native arm64/race suite took 383.750 s (14 cases), including a 21.54 s actual
ENOSPC case on a disposable 256 MiB RAM-backed storage volume: rejected part body
remained unread, committed source outcome survived and full hash matched after
freeing space. Disk-backed restart suites separately passed. Real SQLite
page-limit FULL and actual SDK507 errors are tested too. The HTTP replay test's
clock was aligned with its original fixed fixture after gate launch; the full
ordinary/race/generation suite was rerun. Other production/native/image sources
remained unchanged after their final gate launches.

Native images: storage
`sha256:a1a20ce733c3aa07310dc05575d3c60ee642d0aefb9c8fdcb4ed175a9e1b6ae4`,
media check
`sha256:0308a9be2c3aea20a86b8d261f031de1c0b510cf9cc44c19f3161d5332a03af6`.
Logs: `/tmp/tag-files-capacity-final-{check,media,media-image,storage-image}.log`.
All final check sessions exited 0; disposable containers/volumes were removed.
The earlier focused failure expected `queued` instead of the public `pending`
outcome; its corrected ENOSPC rerun passed 22.079 s. This is not a completion claim
for Task 5 or production: upload HTTP, broader stress/media/browser/Mac, React v27,
offline migration/backup/restore and release containers remain required.

## Upload HTTP and durable Complete (2026-11-15)

Upload JSON routes and raw same-origin part PUT are now wired into the runtime.
Batch creation is SQL-only and admitted against measured capacity; the first
part initializes/reconciles its MPU. Raw bodies bypass JSON decoding/size limits.
Header/length/content checks, shared domain generation/binding preflight and
507 admission occur before body consumption. DTOs expose public IDs/state,
parts and job IDs without keys/MPU credentials. Batches contain pending/error
only. Retry/cancel job controls target upload Complete/ingestion work; cleanup
and deletion remain independent resource protocols. Cancel explicitly returns
ready200 or absent204 when those outcomes win its race.

Schema6 atomically preserves existing jobs and adds versioned `complete` work.
Complete commits the client's generation/ordered selectors and closes admission
before returning202; browser disconnect is no longer the execution boundary.
The worker reconciles/drains, checks the selectors again, fully verifies source
and atomically transfers its lease to ingestion. Startup fences/requeues it;
terminal failure retains input and manual retry reuses session/job identity.
Pending operation targets resolve to durable ready/failure outcomes in the same
publication/failure/cancel transaction. Ready lookup survives session removal
and seven-day receipt expiry/pruning. No session/input TTL was added.

Real regressions reproduced and fixed: stale generation previously initialized
an MPU before rejection (two SDK-peer requests); it now refuses before storage.
A concurrent receipt committed after read-only inspection could previously be
masked by507; admission failure now rechecks/replays that winner. Tests cover
parts larger than the JSON limit, unread invalid/full-disk bodies, immutable
selectors, changed listing, bad source, stale lease, atomic source/job transfer,
retained failure, retry/cancel, pending-only batches, expired ready replay and
known-key conflicts. Binary responses validate against the full source OpenAPI.

Fresh `scripts/check` (generation/gofmt/vet/ordinary/race), expanded
`scripts/check-media` and `scripts/check-media-image` all passed. Full native
arm64/race `TAG_FILES_MEDIA_INTEGRATION=1 scripts/check-storage-image` passed
383.936s, including actual HTTP create/part/Complete, interrupted Complete with
browser closed, reopen/recovery through real S3/FFmpeg primary/poster, final
operation receipt, no upload history, media/readiness/CLI/SIGTERM and owner
reacquisition. That HTTP/runtime case was rerun after the last HTTP-only replay
fix and passed27.107s (case26.06s); unrelated native cases and media sources did
not change after their final gate launch. Focused source verification and race
tests passed too. The startup-recovery and expired-receipt tests required fixture
corrections (reopen before recovery, owned primary and typed request hashing),
without relaxing production rules.

Images: storage
`sha256:5493f8f698c189e70f8316d2b8071ad3cf3f6b137c28cef28db9f9058fbb2157`,
media check
`sha256:60cc10d9ef7d0d40659750c3bb833bcdac11920dd0fa6be1978c99ff9cdcae7d`.
Logs: `/tmp/tag-files-upload-final-{check,media,media-image,storage-image}.log`,
`/tmp/tag-files-upload-native-final-focused.log` and
`/tmp/tag-files-upload-replay-admission-{red,green}.log`. All final sessions exited
0; disposable test resources were removed. Operational receipt housekeeping,
full upload/browser/20GiB/RSS and broader media/Mac acceptance remain open,
alongside React v27, offline migration/backup/restore and release containers.
This is progress on Tasks3–5, not full task/rewrite acceptance.

## Completion boundary

The rewritten CGO server/SPA/FFmpeg image builds and runs with the owned
storage image under root/deploy Compose; actual Chromium/WebKit uploads and
restart checks pass. New verification/publication workflows replace the old
release path. Legacy sources and explicit legacy Compose files remain until
final acceptance. Real user data has not been connected. No F-ID is claimed fully accepted merely because
its domain methods pass: HTTP/browser, S3, migration/restore and release gates
must supply their own evidence before full completion.

## React, offline tools and local images (2026-11-15)

User-authorized parallel work split external migration, cold backup/restore and
browser uploads/viewer into independent owners while the parent integrated UI,
HTTP static serving, operational housekeeping and exact-image Compose tests.
No publication, branch change or real-data access occurred.

The joined hourly runtime loop prunes only expired operation receipts and
navigation snapshots; old failed uploads remain. Real SQLite RED/GREEN, race
checks, `scripts/check` and affected native application recovery passed.

React now uses pinned dependencies/lockfile and generated OpenAPI types with
Query as its shared server cache, URL filters and versioned preferences. v27
CSS/vector sources were forked without editing accepted reference artifacts.
Implemented: sidebar/72px rail, seven thumbnail sizes, grid/list/page selection,
atomic single/bulk editor with focus return, collection search over all records,
organization CRUD, upload collection selection, worker SHA/XHR and durable
receipts, canonical viewer with contexts/zoom/rotate/video keys/three-second
views/favorite/confirmed permanent deletion. YARL3.33.1's actual component gate
showed Cmd-zoom interception and reset-on-rotate, so the native inline viewer
implements the accepted controls without adding that dependency.

Fresh common `scripts/check` passed: generation diff, Go formatting/vet/tests/
race, both external modules, Python host safety and deployment tests, TypeScript,
ESLint,83 web tests and production build. Fourteen catalog/editor/organization/
category/viewer/focus/Clear/seven-size Chromium/WebKit scenarios passed. Screenshots are emitted into ignored `web/test-results`. The synthetic
Go browser fixture uses the actual HTTP API/SQLite and generated PNG media; it
does not stand in for S3 acceptance.

The actual built app+storage pair also passed both browsers: incremental worker
hash, Blob XHR, route change while processing, verified primary publication,
empty ready queue, real image loading, HEAD and Range. Chromium additionally
loses an accepted part response and reconciles without re-uploading it. WebKit
uses native XHR because Playwright's intercepted Blob loses its body in
`route.fetch`; that harness failure was identified as an empty 413 request,
not bypassed in production. Full pair restart retains both primaries and SPA
deep links. A real simultaneous restart exposed transient storage connection
refusals; CLI startup now waits within its existing deadline for network errors,
while permanent schema/configuration errors still fail promptly.

Tools/migrate implements raw archival/frozen source, exhaustive catalog equality,
real version/delete-marker inventory, immutable resumable PUT/MPU and full-hash
verification. A real versioned S3 source behind a GET/HEAD-only gateway copied
12 objects/14,000,086 bytes to the owned target, including lost successful PUT/
Complete responses and all four category result sets; latest race rehearsal
passed in29.62s (race package30.635s). Actual legacy MinIO-instance/full-library evidence remains open.

Tools/backup implements external stopped-pair generations, all hidden/native
state/deployment/credentials, integrity verification, empty-root restore and
physical rebase with fail-closed markers. Latest native race drill passed in
87.434s (85.05s drill), including147 native/archive entries, ten full objects,
partial MPU, ready replay without session, interrupted work and independent
cleanup. Exact image IDs and limits are in `tools/backup/EVIDENCE.md`.
Install/update have18 tests (17 isolated, one opt-in actual Docker case),
including install→tag write→mandatory cold backup→new declared version→healthy
retained ID/value/secret. A stable private sibling flock now owns the complete
install/update lifecycle before reading installed state, preventing a paused
older updater from downgrading a newer completed update. Crash receipts and
symlink/hardlink/path-swap tests also pass.
Same image IDs were used across those declared versions; no future schema or
registry-pull upgrade is implied.

The true20GiB race gate passed in245.55s (package246.577s): exactly
21,474,836,480bytes in640×32MiB parts with two concurrent transfers. Upload took
51.746s; Complete/full verification, real20GiB scratch copy, unsupported-media
failure retention, rehash and explicit cancel/cleanup took164.985s. Idle app
RSS45,043,712bytes, peak54,693,888bytes over4664samples; scratch peaked at exactly
20GiB. Observed native free-space delta22,385,344,512bytes and shared project
filesystem delta43,882,315,776bytes include other filesystem activity and are
observations, not universal space bounds. Cleanup receipts/intents reached zero.
This was actual unsupported media; valid20GiB transcoding/browser is not implied.
The large child binary preceded the subsequent capacity fix; latest-source
startup/recovery on project virtiofs separately passed under race in25.65s.

The large run exposed a real virtiofs capacity-unit error: preferred IO size was used
instead of allocation-fragment size, overstating available bytes256-fold on
this mount. Production measurement now uses positive Frsize with zero-only
Bsize fallback; identity bytes remain unchanged. Dedicated regression and
affected capacity/profile race tests passed. The complete current owned-image
media/runtime race suite then passed in382.461s, with all14 native cases.

Independent UI review closed all five findings: stale upload-list races,
viewer autoplay/fallback, saved category binding/type, global tag statistics
and immutable command keys after corrected input/uncertain responses. Delivery
review also closed legacy parser parity, dispatch-epoch journaling, update
ownership and partial-MPU budget credit. The latter requires source-part SHA
and authoritative listing SHA or a separate durable validated ACK; old ETags
receive no credit. A fresh native exact-logical-budget partial-MPU rehearsal
passed; full physical capacity still gets checked before Complete.

The latest exact arm64 app image passed both browser uploads and pair restart
again (8.8s browser run). Its notices contain38 npm packages with complete texts,
29 runtime Go modules and278 exact OS package versions. Root/deploy Compose
are synchronized; local workflow lint and both Compose configurations pass.

Remaining: mandatory ListParts checksum correction, growth-profile performance,
final independent runtime/release review and broader media/durability gates. Real Mac Safari/Chrome/VoiceOver and physical
power-loss checks remain explicit external acceptance, not Linux-test claims.


## Additional acceptance and review corrections (2026-11-15)

The explicit [F/D evidence matrix](ACCEPTANCE.md) now links implementation and
actual checks for all38 functions and16 decisions, with external gates kept
separate. A fresh independent runtime/release review found two P2 defects:
foreign-platform images could pass update preflight, and publication rebuilt
images after verification. Platform admission now checks the actual Docker
daemon architecture (including aliases) for app/storage/backup before stopping
anything. New regression tests reproduced the old failure; all18 host tests,
including actual Docker install/backup/update, passed in40.506s. Release artifact
promotion is being corrected separately, without publishing.

A [growth-profile performance probe](PERFORMANCE.md) passed with10,000 SQL
items/2,000 tags/100,000 links/350 collections. During actual libx264 encoding,
search p95 was at most24.114ms, atomic two-item writes at most13.168ms, idle API
RSS49.3MiB. All100 writes survived abrupt encode interruption and recovery;
canonical publication/full checks completed4.272s after readiness. These are
Linux SQL/API measurements, not100GB of objects or browser rendering timings.

The independent [amd64 QEMU rehearsal](../../deploy/AMD64-EVIDENCE.md) passed
real browser uploads, H264/AAC encode/poster/full decode and pair restart with
three matching primary hashes. It remains distinct from native amd64 CI.

Strict ListParts consumers now reject missing SHA instead of copying the
expected binding. Three separate RED tests exposed the earlier acceptance of
missing proof in Refresh, unknown-part retirement and frozen Complete recovery;
all now reject it. The full ingestion race suite passed2.217s after updating
older storage-peer fixtures to supply independently expected checksums. The
owned gateway proof journal is still being implemented and must pass native
restart/restore/fault tests before this contract discrepancy is closed.
