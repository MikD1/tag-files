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
- Task 2 in progress: parser/binder/compiler implemented; organization and
  catalog services/API still open. Tasks 3–9 remain open.

## Decisions

- Use the recommended Go + React stack and accepted v27 UI. The provided
  documents already specify the design; implement under the existing request.
- Keep legacy source/build files until replacement acceptance, so reference
  behavior and the release process remain available during development.
- Work directly in main; no worktree, branch publication or real-data writes.

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
execution of compiled golden predicates. This does not prove complete catalog
or HTTP parity yet.

`scripts/check` passed after the binder/compiler changes: generation comparison,
gofmt, vet, Go tests and race tests (search 1.024s, store 2.095s).
The offline run also passed for the foundation/parser.
The environment exports GOROOT for Go 1.27.3; an offline run with the downloaded
1.27.4 binary must unset that inherited GOROOT to avoid mixing toolchains.
