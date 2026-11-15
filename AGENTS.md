# Tag Files

A local media library: Go HTTP application, React SPA, SQLite metadata, private
SeaweedFS object storage, and FFmpeg processing. Read [README.md](README.md) and
[docs/architecture.md](docs/architecture.md) for the runtime and data model;
[docs/operations.md](docs/operations.md) describes commands and configuration.

## Source layout

- `app/`: application, domain modules, SQLite schema/queries, storage gateway,
  media processing, and background workers.
- `web/`: React/TypeScript UI, upload coordinator, API client, and browser tests.
- `api/openapi.yaml`: HTTP contract; generated Go and TypeScript types.
- `tools/migrate/`, `tools/backup/`: separate offline data tools.
- `tools/codegen/`: pinned Go/TypeScript generators.
- `compose.yml`: deployment template used by installation and runtime checks.
- `deploy/`: container builds and distribution notices.
- `scripts/`: generation, checks, installation, updates, and release tooling.

## Engineering boundaries

- Keep domain rules in their owning Go module; transport/storage adapters use
  those rules rather than maintaining independent copies.
- Change `api/openapi.yaml` before changing a public HTTP contract. Schema changes
  belong in additive Goose migrations; static queries belong in sqlc sources.
  Run `scripts/generate`; do not hand-edit generated files.
- SQLite uses the production driver, WAL, FULL synchronization, foreign keys,
  and one immediate writer. S3 and FFmpeg calls stay outside SQL transactions.
- Stream media with bounded buffers. Persist expected sizes and SHA-256 hashes
  before writes; verify full object bytes before publication. Published keys
  and byte descriptors are immutable.
- Preserve job leases, generation fences, idempotency, and independent cleanup
  receipts. Removing an upload session must not remove its cleanup obligations.
- Keep uploads/media on the application origin. Storage remains private and
  browsers never receive storage credentials.
- Preserve integer IDs, duplicate/case-sensitive Unicode tag names, and nullable
  integer 100 ns durations. Do not silently merge or discard data.
- Backup and migration remain external tools, without app endpoints or UI.
- Treat a deployment's SQLite and storage volumes as one paired library. Never
  run tests against an existing library or remove unrelated Docker resources.

## Validation and documentation

Install pinned dependencies with `npm ci --prefix web` and
`npm ci --prefix tools/codegen`. Use `scripts/check` for generation, Go vet/tests/
race tests, tool tests, and frontend checks. `scripts/check-web` runs frontend
checks alone; `npm --prefix web run test:browser` runs the disposable API/browser
suite. Use the focused media/storage/container commands in
[operations](docs/operations.md) when those boundaries change.

Run checks relevant to the change and report actual results. Keep tests,
fixture provenance, lockfiles, schema history, licenses, and distribution notices.
Keep documentation in English, concise, and about implemented behavior. Place
human guides in `docs/` and draw diagrams with Mermaid. Do not commit local
libraries, credentials, build output, test reports, screenshots, or planning
artifacts.
