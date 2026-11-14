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
- Next: real pinned SeaweedFS/SDK conformance, private storage bootstrap and
  preflight, followed by durable ingestion. No executable/SPA image exists yet.

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
