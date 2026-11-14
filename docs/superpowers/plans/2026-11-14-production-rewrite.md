# Tag Files production rewrite

**Goal:** Replace the legacy C#/Angular application completely, with tested
offline migration/backup tools and the existing release-triggered publication
of runnable OCI images. Work locally in main. Do not deploy or publish.

**Specification:** All documents in `docs/rewrite`, including F-01…F-38,
D-01…D-16 and the accepted UI v27 baseline. These are the existing design;
the requested implementation does not reopen product/design decisions.

**Architecture:** Go/net/http modular monolith + React SPA; embedded Goose
migrations, mattn SQLite, generated sqlc queries, spec-first OpenAPI; AWS SDK
V2 and SeaweedFS 4.53 mini. One app image including FFmpeg and SPA, one storage
image. Tools run separately and offline. Dependency versions are pinned after
checking availability and compatibility.

## File ownership

`app/internal/store` owns connections, library lock, migrations and generated
static SQL. `app/internal/search` owns grammar/AST/binding/SQL filter.
`app/internal/organization`, `catalog`, `ingestion`, `media`, `operations` own
their respective rules. `app/internal/httpapi` adapts generated JSON handlers
and raw streams. `web/src` owns router/cache/UI and bounded upload coordinator.
`api/openapi.yaml` owns Go/TS JSON types; `scripts` owns generation/checks and
integration harnesses. `tools` owns migration/archive/restore, with independent
fixtures. `deploy` owns storage preflight/config. Root Docker/Compose/workflows
own installation and release publication.

## Task 1: Durable library foundation

1. Write failing integration tests in `app/internal/store/store_test.go` for
   fresh install/reopen, process ownership, WAL/FULL/FK on all connections,
   exact nullable int64 duration ticks, safe IDs, foreign keys, collection
   removal, incompatible format/schema rejection without writes, and locking
   of BEGIN IMMEDIATE against another SQLite writer.
2. Add `app/go.mod`, pinned driver/Goose, embedded schema history and
   `store.Open(ctx, directory) (*Store,error)`, `Close() error`,
   `Write(ctx,func(*sql.Tx)error) error`. Readers are bounded/read-only,
   writer has one connection; OS lock lives until both pools close.
3. Add sqlc source/config/generated queries for metadata and durable jobs;
   add claim/heartbeat/finish/retry with affected-row fencing and tests for
   crash/reclaim, stale worker, and concurrent claims. Preserve D-15: no TTL
   sessions and no terminal job archive.
4. Run `go test ./internal/store/...`, `go vet ./...`, race suite and generation
   reproducibility. Commit foundation and record exact evidence.

## Task 2: Search and organization semantics

1. Test grammar independently against golden expressions in 01/02, all error
   positions/limits, Unicode and duplicate names, NOT unresolved, precedence.
2. Implement versioned AST, parse/bind and bounded parameterized EXISTS
   compiler in `app/internal/search`. Tests execute filters in real SQLite.
3. Implement organization CRUD and global statistics, bound saved queries.
   Test create expanding duplicate groups, rename preserving result sets
   without rebinding unrelated unresolved names, name-only PATCH preserving
   AST, explicit text edit rebinding, optimistic concurrency and rollback.
4. Catalog/search tests cover all kinds, six sorts (NULL durations/ties),
   seeded Random, pagination >300, exact nonempty similar sets, 0/1 collection,
   favorite set, deduplicated view events and atomic single/bulk replacement.
5. Run Go/static/race/generation checks and commit.

## Task 3: Contract and HTTP commands

1. Write full OpenAPI 3.0.3 for every operation in 02 (including raw streaming),
   Problem Details, field errors, If-Match and Idempotency-Key UUIDs.
2. Pin and generate oapi-codegen/openapi-typescript; wire strict JSON server.
   Add transport tests proving operation/result atomicity, same-key replay,
   conflict on changed request, seven-day operation retention and safe numbers.
3. Add durable bounded navigation snapshots (20k/24h) and tests for stable
   order/page/origin, deleted IDs, expiry and saved query version snapshots.
4. Verify all named API-only functions F-01…F-38 via contract scenarios.

## Task 4: Real storage and ingestion vertical slice

1. Pin Seaweed image/digests; configure mini/LevelDB2, complete /data volume,
   private credentials/network and persisted global fsync preflight. Test
   restart and negative preflight before permitting upload.
2. Write real SDK conformance/fault tests: CRUD, list pagination, auth,
   metadata, Range/conditional/416; nonseekable UploadPart explicit SHA256
   rejecting wrong bytes, immutable part binding and no exhausted-body retry.
3. Implement durable sessions/MPU intents, ordered complete manifest, drain,
   ListParts reconciliation, HEAD + complete GET/hash after every successful
   Complete. Test lost responses/restart/late writer/InvalidPart/cancel.
4. Add fenced immutable candidate publication with expected size/hash before
   PUT. Full GET/hash before atomic ready/session+job removal+cleanup/poster
   commit. Retry of ready resolves items.upload_id. Test stale workers and
   ready/cancel races, independent cleanup and absence of history/session TTL.

Implementation refinement for steps 3–4: persist live/uncertain external-write
intents independently of sessions. The owned storage gateway publishes a random
boot epoch and rejects a stale expected-epoch header before forwarding. This
lets recovery prove an old backend can no longer accept an intent's request;
leases, elapsed age and an empty listing are not settlement evidence. Standard
S3 requests remain compatible; runtime ingestion binds its writes to the epoch
recorded in each intent. Test stale writes across a real storage restart.

## Task 5: Media and recovery

1. Real FFmpeg image/video fixtures test probe allowlist, unsupported input,
   H264/AAC faststart, preserved resolution/orientation and nullable ticks.
2. One process-group wrapper: bounded output/threads/scratch, TERM/grace/KILL,
   Wait/reap, no shell. Test descendant surviving parent and shutdown.
3. Implement poster as independent work; primary remains available on poster
   failure. Media proxy streams GET/HEAD/Range/conditional/416 with safe legacy
   download. Add disk admission (507) and recovery/fault injection at every
   boundary listed in 07, including deletion tombstones/late publication.

## Task 6: React UI v27 and real workflows

1. Pin compatible React/Vite/Router/Query/Mantine/TypeScript + lockfile. Shared
   generated API client, Query cache only, URL codec, versioned preferences.
2. Reproduce v27 sidebar/72px rail, five sections, grid popup/density, list
   actions/page selection and atomic bulk replace; full pagination. Test
   keyboard/focus, stable editor chips/single collection popup and Cancel.
3. Implement one /content/:id viewer; validate candidate viewer library
   against 80dvh layout, image controls, exact video shortcuts, contexts,
   same-tags/collection links, StrictMode-safe three-second views.
4. Implement bounded worker fingerprint/part SHA and XHR Blob streaming,
   progress, retry/reselection identity gate, pending/error polling stopping
   when idle. Add missing legacy CRUD and upload collection workflows.
5. Run tsc/lint/unit/Playwright Chromium+WebKit; record screenshots against
   accepted-baseline.json; real Safari/Chrome/VoiceOver gate remains explicit.

## Task 7: External legacy migration

1. Read 04 and actual EF history/snapshot; build independent source fixtures
   covering every legacy scalar/join/kind/ticks/raw timestamp/duplicate and
   malformed/orphan/missing-object/processing/temporary case.
2. Implement separate read-only scan, frozen source fingerprint, archival
   manifest/raw rows/S3 metadata, disk budget, resumable independent target
   copy/checkpoints and complete verify. Never convert old media or write
   source. Preserve IDs, raw integer ticks, explicit exceptions and rollback.
3. Rehearse full scan/copy/interruption/resume/verify with real S3, compare
   counts/scalars/joins/query ID sets and 100% object SHA; report each loss.

## Task 8: External cold backup/restore

1. Implement host scripts and separate Linux tool image requiring stopped
   app AND storage; full app and storage volumes, metadata/hidden MPU state,
   config/secrets/digests manifest, .part to verified complete generation.
2. Reject incomplete/mutated archives, unsafe paths, running writers and
   nonempty restore targets. Verify SQLite snapshot and full archive hashes.
3. Drill clean-volume restore with legacy/ready/failed/old idle/partial MPU/
   incoming/queued/cleanup fixtures; recovery runs on a disposable second copy.
   Document exact commands, storage preflight and rollback after new writes.

## Task 9: Containers, releases and acceptance

1. Build CGO Go/SPA/FFmpeg app and pinned storage images for arm64+amd64.
   Replace root Compose with app+storage, loopback app only, named data/scratch
   volumes, install/update scripts and incompatible-schema refusal.
2. Preserve GitHub release.published -> GHCR version/latest image publication;
   build/test before publish, test both architectures and generation diff in
   CI. Run the exact built images locally after pull/load, offline after build.
3. Retire old app/build paths only after parity. Update root README/install/
   upgrade/runbooks, full F/D evidence matrix and dependency/license inventory.
4. Run all 07 gates: Mac arm64 Safari/Chrome/VoiceOver/sleep/restart/power loss,
   S3 conformance, full migration/restore rehearsal, memory/latency/disk/recovery
   measurement. Missing evidence stays open. Do not mark goal complete until
   every explicit deliverable is verified against current artifacts/runtime.

## Review focus

- Rename into an unresolved negated name must preserve saved query results.
- Leases do not stop already accepted storage writes; candidate keys must
  never be shared between different bytes or publication generations.
- Ready/cancel remove visible sessions but retain independent cleanup intents.
- Historical int64 ticks cannot pass through float or nanosecond duration.
- Source archive/restore and full native storage state must be verified, not
  inferred from a successful media read or a count-only migration check.

Execution is local and continuous under the user's existing authorization.
Task completion and verification are recorded in `docs/rewrite/progress.md`.
