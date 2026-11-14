# Rewrite implementation progress

Scope remains the full rewrite in [the implementation plan](../superpowers/plans/2026-11-14-production-rewrite.md).
No release, migration of real data, or production-readiness claim has been made.

## Current state (2026-11-14)

- Baseline inspected: clean main at `bc7d62d`; existing app is C#/Angular.
- Requirements inspected: F/D contracts, product specification, target schema,
  storage ADR, delivery gates and external backup/migration boundaries.
- Environment: Linux arm64, Docker daemon available, Go 1.27.3 and Node 26.13.0.
  Real macOS Safari/VoiceOver/power-loss checks cannot be inferred from this VM.
- Task 1 implemented: durable SQLite foundation and generated job queries.
- Task 2 implemented at the application/SQL layer: search, organization CRUD,
  catalog retrieval and atomic metadata/favorite/view commands.
- Task 3 core implemented: full OpenAPI source, generated Go/TypeScript,
  strict JSON adapters for catalog/organization, durable operation results
  and navigation contexts. Upload/media/system handlers and the full API-only
  acceptance matrix remain open alongside Tasks 4–9.
- Task 4 started: a direct S3 adapter has passed real pinned SeaweedFS/SDK
  conformance, including restart and lost responses. A separate read-only storage
  preflight binary has passed real-container positive/negative tests. Production
  private storage bootstrap/readiness wiring and all durable ingestion remain open.
- Next: owned storage bootstrap (fsync before bucket creation), private image/
  readiness wiring, then durable ingestion. No executable/SPA image exists yet.

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
The fixture still creates its bucket before applying fsync; production bootstrap
ordering, actual upload admission and the release storage image remain open.

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
Live-process configuration provenance, bootstrap/image/mounts and gating uploads
remain deferred to deployment wiring; physical flush is not proved here.

Deferred review gates map to the still-open tasks: remaining upload/system/media
handlers and API parity, real S3/media/poster publication, browser view lifecycle,
event retention/performance, external tools/releases, and physical Mac durability.
Manual arbitrary SQL corruption/repair is outside the application contract.

## Completion boundary

There is no runnable rewritten server/SPA image yet. The legacy app/workflows
remain present for reference and are not the requested final result. Real user
data has not been connected. No F-ID is claimed fully accepted merely because
its domain methods pass: HTTP/browser, S3, migration/restore and release gates
must supply their own evidence before full completion.
